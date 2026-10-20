// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/clortho"
)

// testBasicToken is a basic auth token.
type testBasicToken struct {
	user, password string
}

func (t testBasicToken) Principal() string { return t.user }
func (t testBasicToken) UserName() string  { return t.user }
func (t testBasicToken) Password() string  { return t.password }

// testPlainToken is a token with neither a known scheme nor attributes.
type testPlainToken string

func (t testPlainToken) Principal() string { return string(t) }

// testClaimsToken returns a JWT token for "client" with the given claims.
func testClaimsToken(t *testing.T, claims map[string]any) testJWT {
	t.Helper()

	b := jwt.NewBuilder().Subject("client")
	for k, v := range claims {
		b = b.Claim(k, v)
	}

	token, err := b.Build()
	require.NoError(t, err)

	return testJWT{token}
}

func TestDetermineEndpoint(t *testing.T) {
	endpoints := []*regexp.Regexp{
		regexp.MustCompile(`/api/v3/device/[^/]+/config`),
		regexp.MustCompile(`/api/v3/hook[s ]`),
	}

	tests := []struct {
		name      string
		endpoints []*regexp.Regexp
		path      string
		nilReq    bool
		want      string
	}{
		{name: "no endpoints", path: "/api/v3/device/mac:112233445566/config", want: NoneEndpoint},
		{name: "nil request", endpoints: endpoints, nilReq: true, want: NoneEndpoint},
		{name: "match", endpoints: endpoints, path: "/api/v3/device/mac:112233445566/config", want: endpoints[0].String()},
		{name: "match must be at the start", endpoints: endpoints, path: "/prefix/api/v3/device/mac:112233445566/config", want: NotRecognizedEndpoint},
		{name: "no match", endpoints: endpoints, path: "/api/v3/other", want: NotRecognizedEndpoint},
		{name: "spaces become underscores", endpoints: endpoints, path: "/api/v3/hooks", want: "/api/v3/hook[s_]"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var req *http.Request
			if !tc.nilReq {
				req = httptest.NewRequest(http.MethodGet, tc.path, nil)
			}

			assert.Equal(t, tc.want, determineEndpoint(tc.endpoints, req))
		})
	}
}

func TestDeterminePartnerID(t *testing.T) {
	claims := func(partners any) map[string]any {
		return map[string]any{allowedResources: map[string]any{allowedPartners: partners}}
	}

	tests := []struct {
		name  string
		token bascule.Token
		want  string
	}{
		{name: "nil token", want: ""},
		{name: "basic token", token: testBasicToken{user: "user"}, want: ""},
		{name: "unknown token type", token: testPlainToken("client"), want: AuthUnknownScheme},
		{name: "missing claims", token: testClaimsToken(t, nil), want: AuthMissingClaims},
		{name: "invalid claims", token: testClaimsToken(t, claims(map[string]any{"p": true})), want: AuthInvalidClaims},
		{name: "no partners", token: testClaimsToken(t, claims([]string{})), want: NonePartner},
		{name: "one partner", token: testClaimsToken(t, claims([]string{"comcast"})), want: "comcast"},
		{name: "wildcard", token: testClaimsToken(t, claims([]string{Wildcard})), want: WildcardPartner},
		{name: "many partners", token: testClaimsToken(t, claims([]string{"a", "b"})), want: ManyPartner},
		{name: "many partners with a wildcard", token: testClaimsToken(t, claims([]string{"a", Wildcard})), want: WildcardPartner},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, determinePartnerID(tc.token))
		})
	}
}

func TestAuthenticatorEvent_GetLabels(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: basculejwt.ErrExpired, want: AuthUnsatifiedExp},
		{err: basculejwt.ErrInvalidIssuedAt, want: AuthUnsatifiedIAT},
		{err: basculejwt.ErrNotYetValid, want: AuthUnsatifiedNBF},
		{err: clortho.ErrKeyNotFound, want: AuthKeyNotFind},
		{err: basculejwt.ErrInvalidSignature, want: AuthCannotVerify},
		{err: bascule.ErrMissingCredentials, want: AuthMissingCreds},
		{err: bascule.ErrInvalidCredentials, want: AuthInvalidCreds},
		{err: bascule.ErrBadCredentials, want: AuthBadCreds},
		{err: &basculehttp.UnsupportedSchemeError{Scheme: "Digest"}, want: AuthInvalidScheme},
		{err: errAuthEmptyPrincipal, want: AuthEmptyPrincipal},
		{err: errAuthUnknownScheme, want: AuthUnknownScheme},
		{err: errors.New("surprise"), want: UnknownReason},
	}

	ae := authenticatorEvent{l: zap.NewNop()}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v3/device", nil)
			got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
				Source: req,
				Token:  testBasicToken{user: "user"},
				Err:    fmt.Errorf("wrapped: %w", tc.err),
			})

			assert.Equal(t, map[string]string{
				ClientIDLabel:  "user",
				PartnerIDLabel: "",
				EndpointLabel:  NoneEndpoint,
				MethodLabel:    http.MethodPost,
				OutcomeLabel:   Rejected,
				ReasonLabel:    tc.want,
			}, map[string]string(got))
		})
	}

	t.Run("no token", func(t *testing.T) {
		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source: httptest.NewRequest(http.MethodGet, "/", nil),
			Err:    bascule.ErrMissingCredentials,
		})

		assert.Empty(t, got[ClientIDLabel])
		assert.Empty(t, got[PartnerIDLabel])
		assert.Equal(t, AuthMissingCreds, got[ReasonLabel])
	})
}

func TestAuthorizerEvent_GetLabels(t *testing.T) {
	tests := []struct {
		err  error
		want string
	}{
		{err: bascule.ErrBadCredentials, want: AuthBadCreds},
		{err: basculehttp.ErrNoCIDRCapability, want: NoCIDRCapability},
		{err: basculehttp.ErrOriginNotAllowed, want: OriginNotAllowed},
		{err: basculehttp.ErrUnknownOrigin, want: UnknownOrigin},
		{err: errTokenTooOld, want: TokenTooOld},
		{err: errNoIssuedAt, want: NoIssuedAt},
		{err: bascule.ErrUnauthorized, want: NoCapabilitiesMatch},
		// these wrap bascule.ErrBadCredentials but must keep their own reason
		{err: errAuthMissingClaims, want: AuthMissingClaims},
		{err: errAuthInvalidClaims, want: AuthInvalidClaims},
		{err: errors.New("surprise"), want: UnknownReason},
	}

	ae := authorizerEvent{l: zap.NewNop()}
	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			got := ae.getLabels(bascule.AuthorizeEvent[*http.Request]{
				Resource: httptest.NewRequest(http.MethodPatch, "/", nil),
				Token:    testBasicToken{user: "user"},
				Err:      fmt.Errorf("wrapped: %w", tc.err),
			})

			assert.Equal(t, map[string]string{
				ClientIDLabel:  "user",
				PartnerIDLabel: "",
				EndpointLabel:  NoneEndpoint,
				MethodLabel:    http.MethodPatch,
				OutcomeLabel:   Rejected,
				ReasonLabel:    tc.want,
			}, map[string]string(got))
		})
	}
}

// TestAuthorizerEvent_PartnerIDRejectionReason feeds the errors the partner ID
// approver actually returns through the labels.  Its errors wrap
// bascule.ErrBadCredentials, and were once all counted as bad_creds.
func TestAuthorizerEvent_PartnerIDRejectionReason(t *testing.T) {
	tests := []struct {
		name   string
		claims map[string]any
		want   string
	}{
		{
			name: "missing claims",
			want: AuthMissingClaims,
		}, {
			name:   "invalid claims",
			claims: map[string]any{allowedResources: map[string]any{allowedPartners: map[string]any{"p": true}}},
			want:   AuthInvalidClaims,
		}, {
			name:   "no partners",
			claims: map[string]any{allowedResources: map[string]any{allowedPartners: []string{}}},
			want:   AuthInvalidClaims,
		},
	}

	ae := authorizerEvent{l: zap.NewNop()}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			token := testClaimsToken(t, tc.claims)

			err := jwtClaimPartnerIDsValidator(req.Context(), req, token)
			require.Error(t, err)

			got := ae.getLabels(bascule.AuthorizeEvent[*http.Request]{Resource: req, Token: token, Err: err})
			assert.Equal(t, tc.want, got[ReasonLabel])
		})
	}
}

// TestAuthenticatorEvent_ValidatorRejectionReason feeds the errors the
// validators actually return through the labels.  Their errors wrap
// bascule.ErrBadCredentials, and were once all counted as bad_creds.
func TestAuthenticatorEvent_ValidatorRejectionReason(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "basic scheme given a jwt",
			err:  basicSchemeValidator(ctx, nil, testClaimsToken(t, nil)),
			want: AuthUnknownScheme,
		}, {
			name: "bearer scheme given a basic token",
			err:  bearerSchemeValidator(ctx, nil, testBasicToken{user: "user"}),
			want: AuthUnknownScheme,
		}, {
			name: "empty principal",
			err:  authPrincipalValidator(ctx, testPlainToken("")),
			want: AuthEmptyPrincipal,
		},
	}

	ae := authenticatorEvent{l: zap.NewNop()}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Error(t, tc.err)
			require.ErrorIs(t, tc.err, bascule.ErrBadCredentials, "still bad credentials to bascule")

			got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
				Source: httptest.NewRequest(http.MethodGet, "/", nil),
				Err:    tc.err,
			})
			assert.Equal(t, tc.want, got[ReasonLabel])
		})
	}
}
