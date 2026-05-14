// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule/basculehttp"
)

func TestBasicAcquirerAcquire(t *testing.T) {
	tcs := []struct {
		name        string
		acquirer    BasicAcquirer
		expected    string
		expectError string
	}{
		{
			name: "username and password returns basic token",
			acquirer: BasicAcquirer{
				Username: "user",
				Password: "pass",
			},
			expected: "Basic " + basculehttp.BasicAuth("user", "pass"),
		},
		{
			name: "token fallback returns configured token",
			acquirer: BasicAcquirer{
				Token: "Bearer abc123",
			},
			expected: "Bearer abc123",
		},
		{
			name:        "no credentials returns error",
			acquirer:    BasicAcquirer{},
			expectError: "no credentials configured",
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			actual, err := tc.acquirer.Acquire()
			if tc.expectError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectError)
				assert.Empty(t, actual)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestJwtAcquirerAcquire(t *testing.T) {
	tcs := []struct {
		name          string
		authURL       string
		timeout       time.Duration
		buffer        time.Duration
		cachedToken   string
		cachedExpiry  time.Time
		serverStatus  int
		serverBody    string
		twoAcquires   bool
		expected      string
		expectErr     string
		expectedCalls int32
	}{
		{
			name:          "cached token is reused without remote call",
			authURL:       "http://127.0.0.1:1/unreachable",
			timeout:       200 * time.Millisecond,
			buffer:        10 * time.Second,
			cachedToken:   "cached-token",
			cachedExpiry:  time.Now().Add(30 * time.Second),
			expected:      "Bearer cached-token",
			twoAcquires:   true,
			expectedCalls: 0,
		},
		{
			name:          "fetches token and caches for second call",
			timeout:       1 * time.Second,
			buffer:        1 * time.Second,
			serverStatus:  http.StatusOK,
			serverBody:    `{"access_token":"token-1","token_type":"Bearer","expires_in":120}`,
			twoAcquires:   true,
			expected:      "Bearer token-1",
			expectedCalls: 1,
		},
		{
			name:          "non-200 from token endpoint returns error",
			timeout:       1 * time.Second,
			buffer:        1 * time.Second,
			serverStatus:  http.StatusUnauthorized,
			serverBody:    "denied",
			expectErr:     "token endpoint returned status 401",
			expectedCalls: 1,
		},
		{
			name:          "malformed json returns error",
			timeout:       1 * time.Second,
			buffer:        1 * time.Second,
			serverStatus:  http.StatusOK,
			serverBody:    `{"access_token":`,
			expectErr:     "failed to parse token response",
			expectedCalls: 1,
		},
		{
			name:          "missing access token returns error",
			timeout:       1 * time.Second,
			buffer:        1 * time.Second,
			serverStatus:  http.StatusOK,
			serverBody:    `{"token_type":"Bearer","expires_in":60}`,
			expectErr:     "missing access_token",
			expectedCalls: 1,
		},
		{
			name:          "bad auth url returns request creation error",
			authURL:       "://bad-url",
			timeout:       1 * time.Second,
			buffer:        1 * time.Second,
			expectErr:     "failed to create token request",
			expectedCalls: 0,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			var calls int32
			authURL := tc.authURL

			if authURL == "" {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					atomic.AddInt32(&calls, 1)
					w.WriteHeader(tc.serverStatus)
					_, _ = fmt.Fprint(w, tc.serverBody)
				}))
				defer srv.Close()
				authURL = srv.URL
			}

			acquirer := &JwtAcquirer{
				Config: RemoteBearerTokenAcquirerOptions{
					AuthURL: authURL,
					Timeout: tc.timeout,
					Buffer:  tc.buffer,
				},
				token:  tc.cachedToken,
				expiry: tc.cachedExpiry,
			}

			actual, err := acquirer.Acquire()
			if tc.expectErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectErr)
				assert.Equal(t, tc.expectedCalls, atomic.LoadInt32(&calls))
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.expected, actual)

			if tc.twoAcquires {
				second, secondErr := acquirer.Acquire()
				require.NoError(t, secondErr)
				assert.Equal(t, tc.expected, second)
			}

			assert.Equal(t, tc.expectedCalls, atomic.LoadInt32(&calls))
		})
	}
}

func TestPartnerKeys(t *testing.T) {
	tcs := []struct {
		name     string
		expected []string
	}{
		{
			name:     "returns expected partner key aliases",
			expected: []string{"partner-id", "partner-ids", "partnerId", "partnerIds"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			actual := PartnerKeys()
			assert.Equal(t, tc.expected, actual)
			assert.False(t, strings.Contains(strings.Join(actual, ","), " "))
		})
	}
}
