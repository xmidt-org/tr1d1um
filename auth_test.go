// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/golang-jwt/jwt/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/clortho"
	"go.uber.org/zap"
)

type mockResolver struct {
	key        clortho.Key
	resolveErr error
	lastKeyID  string
}

func (m *mockResolver) Resolve(_ context.Context, keyID string) (clortho.Key, error) {
	m.lastKeyID = keyID
	return m.key, m.resolveErr
}

func (m *mockResolver) AddListener(_ clortho.ResolveListener) clortho.CancelListenerFunc {
	return func() {}
}

type mockClorthoKey struct {
	keyID  string
	public crypto.PublicKey
}

func (m *mockClorthoKey) Thumbprint(crypto.Hash) ([]byte, error) { return nil, nil }
func (m *mockClorthoKey) KeyID() string                          { return m.keyID }
func (m *mockClorthoKey) KeyType() string                        { return "RSA" }
func (m *mockClorthoKey) KeyUsage() string                       { return "sig" }
func (m *mockClorthoKey) Raw() interface{}                       { return m.public }
func (m *mockClorthoKey) Public() crypto.PublicKey               { return m.public }

// NOTE: this mock deliberately implements only the clortho.Key interface, the
// same method set as clortho's concrete key type.  Adding a convenience
// accessor here that production keys do not have (an earlier version had
// PublicKey() *rsa.PublicKey) hides real key-extraction bugs from these tests.

type mockUnsupportedClorthoKey struct{}

func (m *mockUnsupportedClorthoKey) Thumbprint(crypto.Hash) ([]byte, error) { return nil, nil }
func (m *mockUnsupportedClorthoKey) KeyID() string                          { return "unsupported" }
func (m *mockUnsupportedClorthoKey) KeyType() string                        { return "UNKNOWN" }
func (m *mockUnsupportedClorthoKey) KeyUsage() string                       { return "sig" }
func (m *mockUnsupportedClorthoKey) Raw() interface{}                       { return "not-a-key" }
func (m *mockUnsupportedClorthoKey) Public() crypto.PublicKey               { return nil }

func signToken(t *testing.T, method jwt.SigningMethod, claims jwt.MapClaims, kid string, key interface{}) string {
	t.Helper()

	token := jwt.NewWithClaims(method, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}

	raw, err := token.SignedString(key)
	require.NoError(t, err)
	return raw
}

func TestJWTToken_Principal(t *testing.T) {
	cases := []struct {
		name      string
		principal string
	}{
		// nolint: goconst
		{name: "returns principal string", principal: "alice"},
		{name: "returns empty principal", principal: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := &JWTToken{principal: tc.principal}
			assert.Equal(t, tc.principal, tok.Principal())
		})
	}
}

func TestCreateAuthMiddleware(t *testing.T) {
	logger := zap.NewNop()
	cases := []struct {
		name      string
		config    clortho.Config
		expectErr bool
	}{
		{
			name: "valid resolver template",
			config: clortho.Config{
				Resolve: clortho.ResolveConfig{Template: "https://keys.example/{keyID}"},
			},
			expectErr: false,
		},
		{
			name: "malformed resolver template",
			config: clortho.Config{
				Resolve: clortho.ResolveConfig{Template: "https://keys.example/{keyID"},
			},
			expectErr: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mw, err := createAuthMiddleware(tc.config, CapabilityConfig{}, InboundAuthConfig{}, logger, nopCapabilityMetric{}, nil)
			if tc.expectErr {
				assert.Error(t, err)
				assert.Nil(t, mw)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, mw)
		})
	}
}

// TestCreateAuthMiddleware_Status pins the status each rejection produces.
//
// Two of these guard removals: the Basic cases guard against re-adding
// basculehttp.WithBasic() without a credential store, and the whole table
// guards the decision to let bascule map errors to statuses rather than
// mapping them here.
func TestCreateAuthMiddleware_Status(t *testing.T) {
	tests := []struct {
		description    string
		authHeader     string
		expectedStatus int
	}{
		{
			description:    "no credentials",
			expectedStatus: http.StatusUnauthorized,
		}, {
			description:    "basic is not an offered scheme",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte("any:thing")),
			expectedStatus: http.StatusUnauthorized,
		}, {
			description:    "basic with empty credentials",
			authHeader:     "Basic " + base64.StdEncoding.EncodeToString([]byte(":")),
			expectedStatus: http.StatusUnauthorized,
		}, {
			description:    "unrecognized scheme",
			authHeader:     "Negotiate abc",
			expectedStatus: http.StatusUnauthorized,
		}, {
			description:    "bearer that is not a JWT",
			authHeader:     "Bearer not-a-jwt",
			expectedStatus: http.StatusBadRequest,
		},
		// NOTE: "Bearer " -- a scheme, one space, and no credential -- panics
		// inside bascule v1.1.1.  ParseAuthorization (basculehttp/
		// authorization.go:37) checks len(scheme) > 0 but not len(v) > 0
		// before indexing v[0], so an empty credential indexes an empty
		// string.  Reachable by any unauthenticated request.  Add a case here
		// once that is fixed upstream.
	}

	mw, err := createAuthMiddleware(clortho.Config{
		Resolve: clortho.ResolveConfig{Template: "https://keys.example/{keyID}"},
	}, CapabilityConfig{}, InboundAuthConfig{}, zap.NewNop(), nopCapabilityMetric{}, nil)
	require.NoError(t, err)
	require.NotNil(t, mw)

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			reached := false
			handler := mw.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, "/device/mac:112233445566/config", nil)
			if tc.authHeader != "" {
				req.Header.Set("Authorization", tc.authHeader)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			assert.False(t, reached, "protected handler must not be reached")
			assert.Equal(t, tc.expectedStatus, rec.Code)
		})
	}
}

func TestJWTTokenParser_Parse(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	hmacSecret := []byte("test-secret")

	cases := []struct {
		name        string
		raw         string
		resolverKey clortho.Key
		resolverErr error
		expectErr   error
		expectUser  string
		expectKeyID string
	}{
		{
			name:      "missing raw token",
			raw:       "",
			expectErr: bascule.ErrMissingCredentials,
		},
		{
			name: "valid token uses sub claim",
			// nolint: goconst
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"sub": "alice"}, "kid-sub", privateKey),
			resolverKey: &mockClorthoKey{keyID: "kid-sub", public: &privateKey.PublicKey},
			expectUser:  "alice",
			expectKeyID: "kid-sub",
		},
		{
			name:        "fallback to user claim",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"user": "bob"}, "kid-user", privateKey),
			resolverKey: &mockClorthoKey{keyID: "kid-user", public: &privateKey.PublicKey},
			expectUser:  "bob",
			expectKeyID: "kid-user",
		},
		{
			name:        "fallback to username claim",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"username": "charlie"}, "kid-username", privateKey),
			resolverKey: &mockClorthoKey{keyID: "kid-username", public: &privateKey.PublicKey},
			expectUser:  "charlie",
			expectKeyID: "kid-username",
		},
		{
			name:        "fallback to unknown principal",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"role": "admin"}, "kid-unknown", privateKey),
			resolverKey: &mockClorthoKey{keyID: "kid-unknown", public: &privateKey.PublicKey},
			expectUser:  "unknown",
			expectKeyID: "kid-unknown",
		},
		{
			name:        "invalid signing method",
			raw:         signToken(t, jwt.SigningMethodHS256, jwt.MapClaims{"sub": "dana"}, "kid-hs", hmacSecret),
			resolverKey: &mockClorthoKey{keyID: "kid-hs", public: &privateKey.PublicKey},
			expectErr:   bascule.ErrInvalidCredentials,
		},
		{
			name:        "missing kid header",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"sub": "erin"}, "", privateKey),
			resolverKey: &mockClorthoKey{keyID: "unused", public: &privateKey.PublicKey},
			expectErr:   bascule.ErrInvalidCredentials,
		},
		{
			name:        "resolver returns error",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"sub": "frank"}, "kid-resolve", privateKey),
			resolverErr: errors.New("resolve failed"),
			expectErr:   bascule.ErrInvalidCredentials,
			expectKeyID: "kid-resolve",
		},
		{
			name:        "unsupported key type",
			raw:         signToken(t, jwt.SigningMethodRS256, jwt.MapClaims{"sub": "grace"}, "kid-unsupported", privateKey),
			resolverKey: &mockUnsupportedClorthoKey{},
			expectErr:   bascule.ErrInvalidCredentials,
			expectKeyID: "kid-unsupported",
		},
		{
			name:      "malformed token",
			raw:       "not.a.valid.jwt",
			expectErr: bascule.ErrInvalidCredentials,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolver := &mockResolver{key: tc.resolverKey, resolveErr: tc.resolverErr}
			parser := &JWTTokenParser{
				resolver: resolver,
				logger:   zap.NewNop(),
			}

			tok, err := parser.Parse(context.Background(), tc.raw)
			if tc.expectErr != nil {
				assert.Error(t, err)
				assert.True(t, errors.Is(err, tc.expectErr))
				assert.Nil(t, tok)
			} else {
				assert.NoError(t, err)
				require.NotNil(t, tok)
				assert.Equal(t, tc.expectUser, tok.Principal())
			}

			if tc.expectKeyID != "" {
				assert.Equal(t, tc.expectKeyID, resolver.lastKeyID)
			}
		})
	}
}
