// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/spiffe/go-spiffe/v2/svid/jwtsvid"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"github.com/xmidt-org/bascule/basculehttp"
)

const (
	// DefaultSPIFFEAssertionType is the client_assertion_type sent with a
	// JWT-SVID when the configuration names none.
	DefaultSPIFFEAssertionType = "urn:ietf:params:oauth:client-assertion-type:spiffe-jwt"

	defaultSPIFFETimeout = 30 * time.Second

	// maxTokenResponseSize bounds how much of the issuer's response is read.
	maxTokenResponseSize = 1 << 20
)

var (
	ErrEmptyTokenURL   = errors.New("empty token url is not valid")
	ErrEmptyClientID   = errors.New("empty client id is not valid")
	ErrInvalidTokenRes = errors.New("invalid token response")
	ErrNoWorkloadAPI   = errors.New("no SPIFFE Workload API address: set socket or " + workloadapi.SocketEnv)
)

// spiffeDecoratorConfig configures a decorator that exchanges the workload's
// JWT-SVID for a bearer token.  The SVID takes the place of a client secret:
// the issuer verifies it against the SPIRE trust bundle and checks that its
// SPIFFE ID is registered to ClientID.
type spiffeDecoratorConfig struct {
	// TokenURL is the issuer's token endpoint.
	TokenURL string

	// ClientID is the issuer's client the SPIFFE ID is registered to.
	ClientID string

	// Scopes are the capabilities to request.  The issuer grants no more
	// than these, so a decorator can ask for a subset of what its client is
	// approved for.  When empty, no scope is sent and the issuer decides.
	Scopes []string

	// Audience is the aud claim requested for the JWT-SVID.  Defaults to
	// TokenURL, which is what issuers check against to prevent replay.
	Audience string

	// AssertionType is sent as client_assertion_type.  Defaults to
	// DefaultSPIFFEAssertionType.
	AssertionType string

	// Socket is the SPIFFE Workload API address, e.g.
	// unix:///run/spire/sockets/agent.sock.  A bare path is taken to be a
	// unix socket.  Defaults to the SPIFFE_ENDPOINT_SOCKET environment
	// variable.
	Socket string

	// Timeout bounds fetching the SVID and exchanging it.  Defaults to 30s.
	Timeout time.Duration

	// Buffer is how long before the token expires to replace it.
	Buffer time.Duration
}

// svidFetcher returns a serialized JWT-SVID for the given audience.
type svidFetcher func(ctx context.Context, audience string) (string, error)

// workloadAPIAddress resolves the Workload API address so that a mistake in
// it is reported at startup rather than on the first outbound request.
func workloadAPIAddress(socket string) (string, error) {
	if socket == "" {
		var ok bool
		if socket, ok = workloadapi.GetDefaultAddress(); !ok || socket == "" {
			return "", ErrNoWorkloadAPI
		}
	}

	if strings.HasPrefix(socket, "/") {
		socket = "unix://" + socket
	}

	if err := workloadapi.ValidateAddress(socket); err != nil {
		return "", fmt.Errorf("invalid SPIFFE Workload API address %q: %w", socket, err)
	}

	return socket, nil
}

func workloadAPIFetcher(addr string) svidFetcher {
	return func(ctx context.Context, audience string) (string, error) {
		svid, err := workloadapi.FetchJWTSVID(ctx, jwtsvid.Params{Audience: audience}, workloadapi.WithAddr(addr))
		if err != nil {
			return "", err
		}

		return svid.Marshal(), nil
	}
}

func newSPIFFEDecorator(cfg spiffeDecoratorConfig, key string, fetch svidFetcher) (*spiffeDecorator, error) {
	if cfg.TokenURL == "" {
		return nil, fmt.Errorf("%w: `%s`", ErrEmptyTokenURL, key)
	}
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("%w: `%s`", ErrEmptyClientID, key)
	}
	if cfg.Audience == "" {
		cfg.Audience = cfg.TokenURL
	}
	if cfg.AssertionType == "" {
		cfg.AssertionType = DefaultSPIFFEAssertionType
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultSPIFFETimeout
	}
	if fetch == nil {
		addr, err := workloadAPIAddress(cfg.Socket)
		if err != nil {
			return nil, fmt.Errorf("%w: `%s`", err, key)
		}
		fetch = workloadAPIFetcher(addr)
	}

	return &spiffeDecorator{
		cfg:        cfg,
		fetch:      fetch,
		now:        time.Now,
		httpClient: &http.Client{Timeout: cfg.Timeout},
	}, nil
}

// spiffeDecorator implements Decorator by exchanging a JWT-SVID for a bearer
// token, which it reuses until it nears expiration.
type spiffeDecorator struct {
	cfg        spiffeDecoratorConfig
	fetch      svidFetcher
	now        func() time.Time
	httpClient *http.Client

	lock  sync.RWMutex
	cache string
	exp   time.Time
}

// Decorate sets the Authorization header from the cached token, exchanging a
// fresh SVID for a new token when the cached one is missing or near expiry.
func (d *spiffeDecorator) Decorate(ctx context.Context, req *http.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	value, ok := d.cached()
	if !ok {
		var err error
		if value, err = d.refresh(ctx); err != nil {
			return err
		}
	}

	req.Header.Set(basculehttp.DefaultAuthorizationHeader, value)

	return nil
}

func (d *spiffeDecorator) cached() (string, bool) {
	d.lock.RLock()
	defer d.lock.RUnlock()

	return d.cache, d.valid()
}

// valid reports whether the cached token is usable.  The lock must be held.
func (d *spiffeDecorator) valid() bool {
	return d.cache != "" && d.now().Add(d.cfg.Buffer).Before(d.exp)
}

func (d *spiffeDecorator) refresh(ctx context.Context) (string, error) {
	d.lock.Lock()
	defer d.lock.Unlock()

	// Another caller may have refreshed while this one waited for the lock.
	if d.valid() {
		return d.cache, nil
	}

	ctx, cancel := context.WithTimeout(ctx, d.cfg.Timeout)
	defer cancel()

	svid, err := d.fetch(ctx, d.cfg.Audience)
	if err != nil {
		return "", fmt.Errorf("error fetching JWT-SVID: %w", err)
	}

	token, expiresIn, err := d.exchange(ctx, svid)
	if err != nil {
		return "", err
	}

	d.cache = fmt.Sprintf("%s %s", basculehttp.SchemeBearer, token)
	d.exp = d.now().Add(expiresIn)

	return d.cache, nil
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

func (d *spiffeDecorator) exchange(ctx context.Context, svid string) (string, time.Duration, error) {
	form := url.Values{
		"client_id":             {d.cfg.ClientID},
		"client_assertion_type": {d.cfg.AssertionType},
		"client_assertion":      {svid},
	}
	if len(d.cfg.Scopes) > 0 {
		form.Set("scope", strings.Join(d.cfg.Scopes, " "))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.cfg.TokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("error exchanging JWT-SVID at '%s': %w", d.cfg.TokenURL, err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxTokenResponseSize))
	if err != nil {
		return "", 0, fmt.Errorf("error reading token response from '%s': %w", d.cfg.TokenURL, err)
	}

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("received non 200 code exchanging JWT-SVID at '%s': code %v: %s",
			d.cfg.TokenURL, resp.Status, strings.TrimSpace(string(body)))
	}

	var tr tokenResponse
	if err := json.Unmarshal(body, &tr); err != nil {
		return "", 0, fmt.Errorf("%w from '%s': %w", ErrInvalidTokenRes, d.cfg.TokenURL, err)
	}
	if tr.AccessToken == "" {
		return "", 0, fmt.Errorf("%w from '%s': missing access_token", ErrInvalidTokenRes, d.cfg.TokenURL)
	}
	if tr.ExpiresIn <= 0 {
		return "", 0, fmt.Errorf("%w from '%s': missing expires_in", ErrInvalidTokenRes, d.cfg.TokenURL)
	}

	return tr.AccessToken, time.Duration(tr.ExpiresIn) * time.Second, nil
}
