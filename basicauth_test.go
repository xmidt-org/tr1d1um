// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/clortho"
	"go.uber.org/zap"
)

func basicHeader(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// encodedCredential is what authx.inbound.basic holds: base64("user:password").
func encodedCredential(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

func TestNewBasicAuthValidator(t *testing.T) {
	tests := []struct {
		description string
		encoded     []string
		expectedErr bool
		expected    map[string]string
	}{
		{
			description: "valid credential",
			encoded:     []string{encodedCredential("user", "pass")},
			expected:    map[string]string{"user": "pass"},
		}, {
			description: "several credentials",
			encoded:     []string{encodedCredential("a", "1"), encodedCredential("b", "2")},
			expected:    map[string]string{"a": "1", "b": "2"},
		}, {
			description: "surrounding whitespace tolerated",
			encoded:     []string{"  " + encodedCredential("user", "pass") + "  "},
			expected:    map[string]string{"user": "pass"},
		}, {
			description: "password may contain colons",
			encoded:     []string{encodedCredential("user", "a:b:c")},
			expected:    map[string]string{"user": "a:b:c"},
		}, {
			description: "empty password permitted",
			encoded:     []string{encodedCredential("user", "")},
			expected:    map[string]string{"user": ""},
		}, {
			description: "no credentials",
			encoded:     nil,
			expected:    map[string]string{},
		}, {
			description: "not base64",
			encoded:     []string{"!!!not base64!!!"},
			expectedErr: true,
		}, {
			description: "missing colon",
			encoded:     []string{base64.StdEncoding.EncodeToString([]byte("nocolon"))},
			expectedErr: true,
		}, {
			description: "empty user name",
			encoded:     []string{encodedCredential("", "pass")},
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			got, err := newBasicAuthValidator(tc.encoded)

			if tc.expectedErr {
				assert.Error(t, err)
				assert.Nil(t, got)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, got)
			assert.Equal(t, tc.expected, got.credentials)
		})
	}
}

// TestAuthMiddleware_BasicAuth exercises the assembled middleware.  It shows
// authentication and authorization are separate gates: correct credentials get
// past the first and are still subject to the second.
func TestAuthMiddleware_BasicAuth(t *testing.T) {
	const path = "/api/v3/device/mac:112233445566/config"

	cases := []struct {
		name       string
		configured []string
		apiDefault []string
		header     string
		wantStatus int
		wantReach  bool
	}{
		{
			name:       "correct credentials, nothing authorizes the request",
			configured: []string{encodedCredential("user", "pass")},
			header:     basicHeader("user", "pass"),
			wantStatus: http.StatusForbidden,
			wantReach:  false,
		},
		{
			name:       "correct credentials and a granting default",
			configured: []string{encodedCredential("user", "pass")},
			apiDefault: []string{"device/.*/config:get"},
			header:     basicHeader("user", "pass"),
			wantStatus: http.StatusOK,
			wantReach:  true,
		},
		{
			name:       "wrong password",
			configured: []string{encodedCredential("user", "pass")},
			apiDefault: []string{"device/.*/config:get"},
			header:     basicHeader("user", "wrong"),
			wantStatus: http.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "unknown user",
			configured: []string{encodedCredential("user", "pass")},
			apiDefault: []string{"device/.*/config:get"},
			header:     basicHeader("nobody", "pass"),
			wantStatus: http.StatusUnauthorized,
			wantReach:  false,
		},
		{
			name:       "no credentials configured, scheme is not offered",
			configured: nil,
			apiDefault: []string{"device/.*/config:get"},
			header:     basicHeader("user", "pass"),
			wantStatus: http.StatusUnauthorized,
			wantReach:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw, err := createAuthMiddleware(
				clortho.Config{Resolve: clortho.ResolveConfig{Template: "https://keys.example/{keyID}"}},
				CapabilityConfig{
					Type:           "enforce",
					Prefix:         capPrefix,
					CheckBasicAuth: true,
					Default:        tc.apiDefault,
				},
				InboundAuthConfig{Basic: tc.configured},
				zap.NewNop(),
				nopCapabilityMetric{},
				nil,
			)
			require.NoError(t, err)

			reached := false
			protected := mw.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			req.Header.Set("Authorization", tc.header)
			rec := httptest.NewRecorder()
			protected.ServeHTTP(rec, req)

			assert.Equal(t, tc.wantReach, reached)
			assert.Equal(t, tc.wantStatus, rec.Code)
		})
	}
}

func TestCreateAuthMiddleware_BasicConfig(t *testing.T) {
	tests := []struct {
		description string
		basic       []string
		expectedErr bool
	}{
		{
			description: "no credentials, scheme not offered",
			basic:       nil,
		}, {
			description: "valid credentials",
			basic:       []string{encodedCredential("user", "pass")},
		}, {
			description: "malformed credential fails at startup",
			basic:       []string{"!!!not base64!!!"},
			expectedErr: true,
		}, {
			description: "credential missing a colon fails at startup",
			basic:       []string{base64.StdEncoding.EncodeToString([]byte("nocolon"))},
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			got, err := createAuthMiddleware(
				clortho.Config{Resolve: clortho.ResolveConfig{Template: "https://keys.example/{keyID}"}},
				CapabilityConfig{},
				InboundAuthConfig{Basic: tc.basic},
				zap.NewNop(),
				nopCapabilityMetric{},
				nil,
			)

			if tc.expectedErr {
				assert.Error(t, err)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}
