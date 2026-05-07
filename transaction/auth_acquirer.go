package transaction

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/xmidt-org/bascule/basculehttp"
)

// AuthAcquirer provides a mechanism to acquire authentication tokens for outbound requests.
// This is used by the translation and stat services to acquire credentials for the XMiDT API.
type AuthAcquirer interface {
	Acquire() (string, error)
}

// BasicAcquirer provides HTTP Basic authentication credentials.
type BasicAcquirer struct {
	Username string
	Password string
	Token    string
}

// Acquire returns the HTTP Basic authentication header value.
// If Username is set, uses basculehttp.BasicAuth to encode credentials as base64.
// Otherwise returns the pre-formatted Token.
func (b *BasicAcquirer) Acquire() (string, error) {
	if b.Username != "" {
		return "Basic " + basculehttp.BasicAuth(b.Username, b.Password), nil
	}

	if b.Token == "" {
		return "", errors.New("BasicAcquirer: no credentials configured (provide Username/Password or Token)")
	}

	return b.Token, nil
}

// Options for JWT token acquisition
type RemoteBearerTokenAcquirerOptions struct {
	AuthURL string        `json:"authURL"`
	Timeout time.Duration `json:"timeout"`
	Buffer  time.Duration `json:"buffer"`
}

// JwtAcquirer obtains a bearer token from remote endpoint.
type JwtAcquirer struct {
	Config RemoteBearerTokenAcquirerOptions
	mu     sync.RWMutex
	token  string
	expiry time.Time
}

// Acquire gets a JWT token from the configured auth URL, using cached token if still valid
func (j *JwtAcquirer) Acquire() (string, error) {
	j.mu.RLock()
	// Check if we have a valid cached token
	if j.token != "" && time.Now().Before(j.expiry) {
		defer j.mu.RUnlock()
		return string(basculehttp.SchemeBearer) + " " + j.token, nil
	}
	j.mu.RUnlock()

	// Token is missing or expired, fetch a new one
	ctx, cancel := context.WithTimeout(context.Background(), j.Config.Timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, j.Config.AuthURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create token request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to fetch token from %s: %w", j.Config.AuthURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token endpoint returned status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int    `json:"expires_in"`
	}

	// Validate the token response
	if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
		return "", fmt.Errorf("failed to parse token response: %w", err)
	}

	if tokenResp.AccessToken == "" {
		return "", errors.New("token response missing access_token field")
	}

	// Cache the valid token with buffer subtracted from expiry
	j.mu.Lock()
	defer j.mu.Unlock()

	j.token = tokenResp.AccessToken
	if tokenResp.ExpiresIn > 0 {
		// Refresh before expiry by subtracting the buffer duration
		expiryTime := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second)
		j.expiry = expiryTime.Add(-j.Config.Buffer)
	} else {
		// If no expiry info, cache for the buffer duration
		j.expiry = time.Now().Add(j.Config.Buffer)
	}

	return string(basculehttp.SchemeBearer) + " " + j.token, nil
}

// PartnerKeys returns the expected keys for partner information in JWT tokens
func PartnerKeys() []string {
	return []string{"partner-id", "partner-ids", "partnerId", "partnerIds"}
}
