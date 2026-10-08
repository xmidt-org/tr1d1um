// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/spiffe/go-spiffe/v2/workloadapi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSVID          = "header.payload.signature"
	testClientID      = "tr1d1um"
	testIssuerURL     = "https://issuer.example.com"
	testAssertionType = "urn:example:assertion"
)

// fakeIssuer is a token endpoint that records what it was sent.
type fakeIssuer struct {
	t       *testing.T
	calls   atomic.Int32
	status  int
	body    string
	lock    sync.Mutex
	lastReq *http.Request
	form    map[string][]string
}

func (f *fakeIssuer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n := f.calls.Add(1)
	require.NoError(f.t, r.ParseForm())

	f.lock.Lock()
	f.lastReq = r
	f.form = r.PostForm
	f.lock.Unlock()

	if f.status != 0 && f.status != http.StatusOK {
		w.WriteHeader(f.status)
		fmt.Fprint(w, f.body)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if f.body != "" {
		fmt.Fprint(w, f.body)
		return
	}
	fmt.Fprintf(w, `{"access_token":"token-%d","token_type":"Bearer","expires_in":7200,"scope":"a"}`, n)
}

func newTestSPIFFEDecorator(t *testing.T, cfg spiffeDecoratorConfig, issuer *fakeIssuer) (*spiffeDecorator, *[]string) {
	srv := httptest.NewServer(issuer)
	t.Cleanup(srv.Close)

	if cfg.TokenURL == "" {
		cfg.TokenURL = srv.URL
	}
	if cfg.ClientID == "" {
		cfg.ClientID = testClientID
	}

	var audiences []string
	var lock sync.Mutex
	fetch := func(_ context.Context, audience string) (string, error) {
		lock.Lock()
		defer lock.Unlock()
		audiences = append(audiences, audience)
		return testSVID, nil
	}

	d, err := newSPIFFEDecorator(cfg, "test", fetch)
	require.NoError(t, err)

	return d, &audiences
}

func decorate(d Decorator) (string, error) {
	req := httptest.NewRequest(http.MethodGet, "http://xmidt.example.com", nil)
	err := d.Decorate(context.Background(), req)

	return req.Header.Get("Authorization"), err
}

func TestSPIFFEDecorator_Exchange(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	d, audiences := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{
		Scopes: []string{"x1:webpa:api:device/.*/config:get", "x1:webpa:rate:50/1s"},
	}, issuer)

	got, err := decorate(d)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token-1", got)

	assert.Equal(t, []string{d.cfg.TokenURL}, *audiences, "the SVID audience defaults to the token URL")
	assert.Equal(t, http.MethodPost, issuer.lastReq.Method)
	assert.Equal(t, "application/x-www-form-urlencoded", issuer.lastReq.Header.Get("Content-Type"))
	assert.Equal(t, map[string][]string{
		"client_id":             {testClientID},
		"client_assertion_type": {DefaultSPIFFEAssertionType},
		"client_assertion":      {testSVID},
		"scope":                 {"x1:webpa:api:device/.*/config:get x1:webpa:rate:50/1s"},
	}, issuer.form)
}

func TestSPIFFEDecorator_Overrides(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	d, audiences := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{
		Audience:      "https://other.example.com",
		AssertionType: testAssertionType,
	}, issuer)

	_, err := decorate(d)
	require.NoError(t, err)

	assert.Equal(t, []string{"https://other.example.com"}, *audiences)
	assert.Equal(t, []string{testAssertionType}, issuer.form["client_assertion_type"])
	assert.NotContains(t, issuer.form, "scope", "no scope is sent when none is configured")
}

func TestSPIFFEDecorator_Caching(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	d, _ := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{Buffer: 2 * time.Minute}, issuer)

	now := time.Now()
	d.now = func() time.Time { return now }

	for range 3 {
		got, err := decorate(d)
		require.NoError(t, err)
		assert.Equal(t, "Bearer token-1", got)
	}
	assert.EqualValues(t, 1, issuer.calls.Load())

	// Inside the buffer before the 2h expiry, the token is replaced.
	now = now.Add(2*time.Hour - time.Minute)
	got, err := decorate(d)
	require.NoError(t, err)
	assert.Equal(t, "Bearer token-2", got)
	assert.EqualValues(t, 2, issuer.calls.Load())
}

func TestSPIFFEDecorator_Concurrent(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	d, _ := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{}, issuer)

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			got, err := decorate(d)
			assert.NoError(t, err)
			assert.Equal(t, "Bearer token-1", got)
		})
	}
	wg.Wait()

	assert.EqualValues(t, 1, issuer.calls.Load())
}

func TestSPIFFEDecorator_Errors(t *testing.T) {
	cases := []struct {
		name    string
		status  int
		body    string
		wantErr error
		wantMsg string
	}{
		{name: "forbidden", status: http.StatusForbidden, body: `{"error":"scope not approved"}`, wantMsg: "scope not approved"},
		{name: "not json", body: "nope", wantErr: ErrInvalidTokenRes},
		{name: "no token", body: `{"expires_in":7200}`, wantErr: ErrInvalidTokenRes},
		{name: "no expiry", body: `{"access_token":"t"}`, wantErr: ErrInvalidTokenRes},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d, _ := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{}, &fakeIssuer{t: t, status: tc.status, body: tc.body})

			got, err := decorate(d)
			require.Error(t, err)
			assert.Empty(t, got)
			if tc.wantErr != nil {
				assert.ErrorIs(t, err, tc.wantErr)
			}
			if tc.wantMsg != "" {
				assert.ErrorContains(t, err, tc.wantMsg)
			}
		})
	}
}

func TestSPIFFEDecorator_FetchError(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	srv := httptest.NewServer(issuer)
	t.Cleanup(srv.Close)

	errNoAgent := errors.New("no agent")
	d, err := newSPIFFEDecorator(spiffeDecoratorConfig{TokenURL: srv.URL, ClientID: "c"}, "test",
		func(context.Context, string) (string, error) { return "", errNoAgent })
	require.NoError(t, err)

	_, err = decorate(d)
	assert.ErrorIs(t, err, errNoAgent)
	assert.Zero(t, issuer.calls.Load())
}

func TestSPIFFEDecorator_CanceledContext(t *testing.T) {
	d, _ := newTestSPIFFEDecorator(t, spiffeDecoratorConfig{}, &fakeIssuer{t: t})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := d.Decorate(ctx, httptest.NewRequest(http.MethodGet, "http://xmidt.example.com", nil))
	assert.ErrorIs(t, err, context.Canceled)
}

func TestNewSPIFFEDecorator_Validation(t *testing.T) {
	_, err := newSPIFFEDecorator(spiffeDecoratorConfig{ClientID: "c"}, "k", nil)
	assert.ErrorIs(t, err, ErrEmptyTokenURL)

	_, err = newSPIFFEDecorator(spiffeDecoratorConfig{TokenURL: testIssuerURL}, "k", nil)
	assert.ErrorIs(t, err, ErrEmptyClientID)

	d, err := newSPIFFEDecorator(spiffeDecoratorConfig{
		TokenURL: testIssuerURL,
		ClientID: "c",
		Socket:   "unix:///run/spire/sockets/agent.sock",
	}, "k", nil)
	require.NoError(t, err)
	assert.Equal(t, testIssuerURL, d.cfg.Audience)
	assert.Equal(t, DefaultSPIFFEAssertionType, d.cfg.AssertionType)
	assert.Equal(t, defaultSPIFFETimeout, d.cfg.Timeout)
	assert.NotNil(t, d.fetch)
}

func TestWorkloadAPIAddress(t *testing.T) {
	const agentURI = "unix:///run/spire/agent.sock"

	cases := []struct {
		name    string
		socket  string
		env     *string
		want    string
		wantErr error
		failure bool
	}{
		{name: "unix URI", socket: agentURI, want: agentURI},
		{name: "bare path", socket: "/run/spire/agent.sock", want: agentURI},
		{name: "tcp", socket: "tcp://127.0.0.1:8081", want: "tcp://127.0.0.1:8081"},
		{name: "from env", env: ptr("unix:///env/agent.sock"), want: "unix:///env/agent.sock"},
		{name: "bare path from env", env: ptr("/env/agent.sock"), want: "unix:///env/agent.sock"},
		{name: "config wins over env", socket: "/cfg.sock", env: ptr("/env.sock"), want: "unix:///cfg.sock"},
		{name: "nothing set", wantErr: ErrNoWorkloadAPI},
		{name: "empty env", env: ptr(""), wantErr: ErrNoWorkloadAPI},
		{name: "relative path", socket: "run/spire/agent.sock", failure: true},
		{name: "unknown scheme", socket: "http://agent", failure: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.env != nil {
				t.Setenv(workloadapi.SocketEnv, *tc.env)
			} else {
				// t.Setenv restores the variable afterwards; unset it within.
				t.Setenv(workloadapi.SocketEnv, "")
				os.Unsetenv(workloadapi.SocketEnv)
			}

			got, err := workloadAPIAddress(tc.socket)
			switch {
			case tc.wantErr != nil:
				assert.ErrorIs(t, err, tc.wantErr)
			case tc.failure:
				assert.Error(t, err)
			default:
				require.NoError(t, err)
				assert.Equal(t, tc.want, got)
			}
		})
	}
}

func TestNewSPIFFEDecorator_BadSocketFailsAtStartup(t *testing.T) {
	_, err := newSPIFFEDecorator(spiffeDecoratorConfig{
		TokenURL: testIssuerURL,
		ClientID: "c",
		Socket:   "http://agent",
	}, "auth.outbound.fanout.SPIFFE", nil)

	require.Error(t, err)
	assert.ErrorContains(t, err, "auth.outbound.fanout.SPIFFE")
}

// TestSPIFFEDecorator_WorkloadAPI runs the real go-spiffe client against the
// mock Workload API, then exchanges the SVID it gets with the fake issuer.
func TestSPIFFEDecorator_WorkloadAPI(t *testing.T) {
	for _, scheme := range []string{"", "unix://"} {
		t.Run("socket "+scheme+"path", func(t *testing.T) {
			agent := newMockWorkloadAPI(t)
			socket := scheme + agent.Socket

			issuer := &fakeIssuer{t: t}
			srv := httptest.NewServer(issuer)
			t.Cleanup(srv.Close)

			d, err := newSPIFFEDecorator(spiffeDecoratorConfig{
				TokenURL: srv.URL,
				ClientID: testClientID,
				Scopes:   []string{"one:thing"},
				Socket:   socket,
			}, "test", nil)
			require.NoError(t, err)

			got, err := decorate(d)
			require.NoError(t, err)
			assert.Equal(t, "Bearer token-1", got)

			issued, audiences := agent.Issued()
			require.Len(t, issued, 1)
			assert.Equal(t, [][]string{{srv.URL}}, audiences, "the SVID is requested for the token URL")
			assert.Equal(t, []string{issued[0]}, issuer.form["client_assertion"], "the agent's SVID is what is exchanged")
			assert.Equal(t, mockSPIFFEID, jwtClaims(t, issued[0])["sub"])
		})
	}
}

func TestSPIFFEDecorator_WorkloadAPIUnavailable(t *testing.T) {
	issuer := &fakeIssuer{t: t}
	srv := httptest.NewServer(issuer)
	t.Cleanup(srv.Close)

	d, err := newSPIFFEDecorator(spiffeDecoratorConfig{
		TokenURL: srv.URL,
		ClientID: testClientID,
		Socket:   "/nonexistent/agent.sock",
		Timeout:  time.Second,
	}, "test", nil)
	require.NoError(t, err)

	_, err = decorate(d)
	assert.ErrorContains(t, err, "error fetching JWT-SVID")
	assert.Zero(t, issuer.calls.Load())
}

func TestSPIFFEConfigFromYAML(t *testing.T) {
	const config = `
auth:
  outbound:
    fanout:
      spiffe:
        tokenURL: "https://issuer.example.com/v2/oauth/token"
        clientID: "tr1d1um-client"
        scopes:
          - "one:thing"
          - "another:thing"
        audience: "https://issuer.example.com"
        assertionType: "urn:example:assertion"
        socket: "/run/spire/sockets/agent.sock"
        timeout: 10s
        buffer: 2m
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg outboundConfig
	require.NoError(t, v.UnmarshalKey(outboundConfigKey, &cfg))

	assert.Equal(t, spiffeDecoratorConfig{
		TokenURL:      testIssuerURL + "/v2/oauth/token",
		ClientID:      "tr1d1um-client",
		Scopes:        []string{"one:thing", "another:thing"},
		Audience:      testIssuerURL,
		AssertionType: testAssertionType,
		Socket:        "/run/spire/sockets/agent.sock",
		Timeout:       10 * time.Second,
		Buffer:        2 * time.Minute,
	}, cfg.Fanout.SPIFFE)

	d, err := NewDecorator(cfg.Fanout, v, fanoutConfigKey)
	require.NoError(t, err)
	assert.IsType(t, &spiffeDecorator{}, d)
}

// TestSPIFFEDecorator_Staging exchanges a real SVID with a real issuer.  It
// is skipped unless the environment names both, so it runs only where a
// SPIRE agent is reachable, e.g. in a pod with the agent sidecar:
//
//	SPIFFE_ENDPOINT_SOCKET=unix:///run/spire/sockets/agent.sock \
//	TR1D1UM_SPIFFE_TOKEN_URL=https://issuer/v2/oauth/token \
//	TR1D1UM_SPIFFE_CLIENT_ID=my-client \
//	TR1D1UM_SPIFFE_SCOPES="one:thing" \
//	go test ./auth -run Staging -v
//
// It logs the claims of the token it receives, so the capabilities the
// requested scopes produced can be checked by eye.
func TestSPIFFEDecorator_Staging(t *testing.T) {
	tokenURL := os.Getenv("TR1D1UM_SPIFFE_TOKEN_URL")
	clientID := os.Getenv("TR1D1UM_SPIFFE_CLIENT_ID")
	if tokenURL == "" || clientID == "" {
		t.Skip("set TR1D1UM_SPIFFE_TOKEN_URL and TR1D1UM_SPIFFE_CLIENT_ID to run against a real issuer")
	}

	d, err := newSPIFFEDecorator(spiffeDecoratorConfig{
		TokenURL: tokenURL,
		ClientID: clientID,
		Scopes:   strings.Fields(os.Getenv("TR1D1UM_SPIFFE_SCOPES")),
	}, "staging", nil)
	require.NoError(t, err)

	got, err := decorate(d)
	require.NoError(t, err)

	token, ok := strings.CutPrefix(got, "Bearer ")
	require.True(t, ok, "expected a bearer token, got %q", got)

	claims := jwtClaims(t, token)
	pretty, err := json.MarshalIndent(claims, "", "  ")
	require.NoError(t, err)
	t.Logf("token claims:\n%s", pretty)
	t.Logf("capabilities: %v", claims["capabilities"])
}

// jwtClaims decodes a JWT's claims without verifying it.
func jwtClaims(t *testing.T, token string) map[string]any {
	t.Helper()

	parts := strings.Split(token, ".")
	require.Len(t, parts, 3, "not a compact JWT")

	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	require.NoError(t, err)

	var claims map[string]any
	require.NoError(t, json.Unmarshal(payload, &claims))

	return claims
}

func ptr[T any](v T) *T { return &v }
