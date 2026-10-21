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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/clortho"
	"github.com/xmidt-org/tokenrate"
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
				ClientIDLabel:  Unauthenticated,
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

	t.Run("jwt rejected for its claims", func(t *testing.T) {
		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source:       httptest.NewRequest(http.MethodGet, "/", nil),
			InvalidToken: testClaimsToken(t, map[string]any{allowedResources: partners("partner-a")}),
			Err:          basculejwt.ErrExpired,
		})

		assert.Equal(t, "client", got[ClientIDLabel])
		assert.Equal(t, "partner-a", got[PartnerIDLabel])
		assert.Equal(t, AuthUnsatifiedExp, got[ReasonLabel])
	})

	t.Run("token wins over the invalid token", func(t *testing.T) {
		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source:       httptest.NewRequest(http.MethodGet, "/", nil),
			Token:        testClaimsToken(t, nil),
			InvalidToken: testBasicToken{user: "other"},
			Err:          errAuthEmptyPrincipal,
		})

		assert.Equal(t, "client", got[ClientIDLabel])
	})

	t.Run("invalid basic token", func(t *testing.T) {
		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source:       httptest.NewRequest(http.MethodGet, "/", nil),
			InvalidToken: testBasicToken{user: "user"},
			Err:          bascule.ErrBadCredentials,
		})

		assert.Equal(t, Unauthenticated, got[ClientIDLabel])
	})

	t.Run("verified jwt token", func(t *testing.T) {
		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source: httptest.NewRequest(http.MethodGet, "/", nil),
			Token:  testClaimsToken(t, map[string]any{allowedResources: partners("partner-a")}),
			Err:    errAuthEmptyPrincipal,
		})

		assert.Equal(t, "client", got[ClientIDLabel])
		assert.Equal(t, "partner-a", got[PartnerIDLabel])
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

			// a basic user name is unverified when its credentials are bad
			client := "user"
			if tc.want == AuthBadCreds {
				client = Unauthenticated
			}

			assert.Equal(t, map[string]string{
				ClientIDLabel:  client,
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

// TestEventListeners_MissingFields checks that an event missing a field bascule
// documents as always set is still counted, with blank labels, rather than
// panicking on the request goroutine.
func TestEventListeners_MissingFields(t *testing.T) {
	// a missing value is recorded as EmptyLabelValue
	labels := func(outcome, reason, client, endpoint, method string) prometheus.Labels {
		orEmpty := func(v string) string {
			if v == "" {
				return EmptyLabelValue
			}
			return v
		}

		return prometheus.Labels{
			OutcomeLabel:   outcome,
			ReasonLabel:    orEmpty(reason),
			ClientIDLabel:  orEmpty(client),
			PartnerIDLabel: EmptyLabelValue,
			EndpointLabel:  endpoint,
			MethodLabel:    orEmpty(method),
		}
	}

	t.Run("authenticator without a source", func(t *testing.T) {
		counter := testCounter()
		ae := authenticatorEvent{l: zap.NewNop(), counter: counter}

		require.NotPanics(t, func() {
			ae.OnEvent(bascule.AuthenticateEvent[*http.Request]{Err: bascule.ErrMissingCredentials})
		})
		assert.Equal(t, float64(1), testutil.ToFloat64(counter.With(labels(Rejected, AuthMissingCreds, "", NoneEndpoint, ""))))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	token := testBasicToken{user: "user"}
	warning := bascule.Warning{Reason: warningWouldReject, Attrs: []bascule.WarningAttr{{Key: KindLabel, Value: "cidr"}}}

	tests := []struct {
		name  string
		event bascule.AuthorizeEvent[*http.Request]
		want  prometheus.Labels
	}{
		{
			name:  "authorizer rejection without a token",
			event: bascule.AuthorizeEvent[*http.Request]{Resource: req, Err: bascule.ErrUnauthorized},
			want:  labels(Rejected, NoCapabilitiesMatch, "", NoneEndpoint, http.MethodGet),
		}, {
			name:  "authorizer rejection without a resource",
			event: bascule.AuthorizeEvent[*http.Request]{Token: token, Err: bascule.ErrUnauthorized},
			want:  labels(Rejected, NoCapabilitiesMatch, "user", NoneEndpoint, ""),
		}, {
			name:  "authorizer acceptance without a token",
			event: bascule.AuthorizeEvent[*http.Request]{Resource: req},
			want:  labels(Accepted, "", "", NoneEndpoint, http.MethodGet),
		}, {
			name:  "authorizer acceptance without a resource",
			event: bascule.AuthorizeEvent[*http.Request]{Token: token},
			want:  labels(Accepted, "", "user", NoneEndpoint, ""),
		}, {
			name:  "authorizer warning without a token",
			event: bascule.AuthorizeEvent[*http.Request]{Resource: req, Warnings: []bascule.Warning{warning}},
			want:  labels(Accepted, "", "", NoneEndpoint, http.MethodGet),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			counter, warnings := testCounter(), testWarningCounter()
			ae := authorizerEvent{l: zap.NewNop(), counter: counter, warnings: warnings}

			require.NotPanics(t, func() { ae.OnEvent(tc.event) })
			assert.Equal(t, float64(1), testutil.ToFloat64(counter.With(tc.want)))
			assert.Equal(t, len(tc.event.Warnings), testutil.CollectAndCount(warnings), "warnings counted")
		})
	}
}

func TestSanitize(t *testing.T) {
	limit := defaultMaxValueLen

	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "short", in: "user", want: "user"},
		{name: "exactly the limit", in: strings.Repeat("a", limit), want: strings.Repeat("a", limit)},
		{name: "one over", in: strings.Repeat("a", limit+1), want: strings.Repeat("a", limit) + "..."},
		{name: "far over", in: strings.Repeat("a", 1<<20), want: strings.Repeat("a", limit) + "..."},
		// "é" is two bytes, and the limit falls between them
		{name: "cut between the bytes of a character", in: strings.Repeat("a", limit-1) + "é" + "bbb", want: strings.Repeat("a", limit-1) + "..."},
		{name: "a character ending at the limit is kept", in: strings.Repeat("a", limit-2) + "é" + "bbb", want: strings.Repeat("a", limit-2) + "é..."},
		// a run of bytes that are not text is one replacement character
		{name: "bytes that are not text", in: strings.Repeat("\x80", limit+10), want: "\uFFFD"},
		// each invalid byte here becomes 3, so the value is cleaned before it is
		// measured: cutting first would leave it three times too long
		{name: "replacement makes it longer", in: strings.Repeat("\xffa", limit/2), want: strings.Repeat("\uFFFDa", 64) + "..."},
		{name: "cut inside a replacement character", in: strings.Repeat("a", limit-2) + "\xff" + "tail", want: strings.Repeat("a", limit-2) + "..."},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitize(tc.in, limit)
			assert.Equal(t, tc.want, got)
			assert.LessOrEqual(t, len(got), limit+len("..."))
			assert.True(t, utf8.ValidString(got))
		})
	}

	t.Run("unlimited is cleaned but not cut", func(t *testing.T) {
		assert.Equal(t, strings.Repeat("a", 1<<20)+"\uFFFD", sanitize(strings.Repeat("a", 1<<20)+"\xff", unlimited))
	})
}

// TestRejectionLogsShortenThePrincipal checks that a rejected request's
// principal, which for basic auth is whatever the caller sent, is cut before it
// is logged, and that the client label is not it.
func TestRejectionLogsShortenThePrincipal(t *testing.T) {
	user := strings.Repeat("u", 100_000)
	logged := func(logs *observer.ObservedLogs) string {
		require.Equal(t, 1, logs.Len())
		return logs.All()[0].ContextMap()["jwt_principal"].(string)
	}

	t.Run("authenticator", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		ae := authenticatorEvent{l: zap.New(core)}

		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source: httptest.NewRequest(http.MethodGet, "/", nil),
			Token:  testBasicToken{user: user},
			Err:    bascule.ErrBadCredentials,
		})

		assert.Equal(t, Unauthenticated, got[ClientIDLabel])
		assert.Equal(t, sanitize(user, defaultMaxValueLen), logged(logs))
		assert.Len(t, logged(logs), defaultMaxValueLen+len("..."))
	})

	t.Run("authorizer", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		ae := authorizerEvent{l: zap.New(core)}

		got := ae.getLabels(bascule.AuthorizeEvent[*http.Request]{
			Resource: httptest.NewRequest(http.MethodGet, "/", nil),
			Token:    testBasicToken{user: user},
			Err:      bascule.ErrBadCredentials,
		})

		assert.Equal(t, Unauthenticated, got[ClientIDLabel])
		assert.Len(t, logged(logs), defaultMaxValueLen+len("..."))
	})
}

// hugeJWT is a verified-looking token whose principal and partner are as long
// as an issuer could make them.
func hugeJWT(t *testing.T, value string) testJWT {
	t.Helper()

	token, err := jwt.NewBuilder().Subject(value).Claim(allowedResources, partners(value)).Build()
	require.NoError(t, err)

	return testJWT{token}
}

// logField returns a string field of the only entry logged.
func logField(t *testing.T, logs *observer.ObservedLogs, key string) string {
	t.Helper()

	require.Equal(t, 1, logs.Len())
	field, ok := logs.All()[0].ContextMap()[key].(string)
	require.True(t, ok, "%s should be logged", key)

	return field
}

// TestCleanLabels_BoundsValues checks that the client and partner, which come
// from a token or a request, are cut, and that the labels this service sets
// keep their length.
func TestCleanLabels_BoundsValues(t *testing.T) {
	long := strings.Repeat("x", 100_000)
	got := limits{}.cleanLabels(prometheus.Labels{
		ClientIDLabel: long,
		// the replacement character, 3 bytes, starts at byte 254, so the limit
		// falls inside it
		PartnerIDLabel: strings.Repeat("a", 254) + "\xff" + "tail",
		ReasonLabel:    "short",
		MethodLabel:    "",
		// a configured endpoint can be as long as its pattern
		EndpointLabel: strings.Repeat("e", 1000),
		KindLabel:     strings.Repeat("\xff", 100_000), // a run is one replacement
	})

	assert.Equal(t, strings.Repeat("x", defaultMaxValueLen)+"...", got[ClientIDLabel])
	assert.Equal(t, strings.Repeat("a", 254)+"...", got[PartnerIDLabel], "cut on a character boundary")
	assert.True(t, utf8.ValidString(got[PartnerIDLabel]))
	assert.Equal(t, strings.Repeat("e", 1000), got[EndpointLabel], "not a value from a caller")
	assert.Equal(t, "\uFFFD", got[KindLabel], "cleaned, whatever the label")
	assert.Equal(t, "short", got[ReasonLabel])
	assert.Equal(t, EmptyLabelValue, got[MethodLabel])
}

func TestNewLimits(t *testing.T) {
	tests := []struct {
		name               string
		value, warning     int
		wantValue, wantMsg int
		wantErr            string
	}{
		{name: "defaults", wantValue: defaultMaxValueLen, wantMsg: defaultMaxWarningLen},
		{name: "configured", value: 32, warning: 200, wantValue: 32, wantMsg: 200},
		{name: "only the value", value: 32, wantValue: 32, wantMsg: defaultMaxWarningLen},
		{name: "only the warning", warning: 200, wantValue: defaultMaxValueLen, wantMsg: 200},
		{name: "negative value", value: -1, wantErr: "auth.inbound.maxValueLen"},
		{name: "negative warning", warning: -1, wantErr: "auth.inbound.maxWarningLen"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := newLimits(tc.value, tc.warning)
			if tc.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tc.wantErr)

				return
			}

			require.NoError(t, err)

			long := strings.Repeat("a", 5000)
			assert.Equal(t, strings.Repeat("a", tc.wantValue)+"...", l.value(long))
			assert.Equal(t, strings.Repeat("a", tc.wantMsg)+"...", l.warning(long))
		})
	}
}

func TestLimitsCleanLabels(t *testing.T) {
	l, err := newLimits(4, 0)
	require.NoError(t, err)

	got := l.cleanLabels(prometheus.Labels{
		ClientIDLabel:  "abcdefgh",
		PartnerIDLabel: "ab",
		EndpointLabel:  "/a/long/configured/endpoint",
	})

	assert.Equal(t, "abcd...", got[ClientIDLabel])
	assert.Equal(t, "ab", got[PartnerIDLabel])
	assert.Equal(t, "/a/long/configured/endpoint", got[EndpointLabel], "only the client and partner are cut")
}

func TestInboundLimitsFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    maxValueLen: 64
    maxWarningLen: 512
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg inboundConfig
	require.NoError(t, v.UnmarshalKey(inboundConfigKey, &cfg))

	assert.Equal(t, 64, cfg.MaxValueLen)
	assert.Equal(t, 512, cfg.MaxWarningLen)
}

// TestNewMiddleware_ConfiguredLimitsReachEveryCheck checks that the configured
// length is used by the parameter check and the rate limiter too, which are
// given it separately from the authenticator and authorizer.
func TestNewMiddleware_ConfiguredLimitsReachEveryCheck(t *testing.T) {
	keys := newTestKeys(t)
	principal := strings.Repeat("p", 50)
	name := strings.Repeat("n", 50)
	future := time.Now().Add(time.Hour)
	cut := func(c string) string { return strings.Repeat(c, 8) + "..." }

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	v.Set(paramPrefixesKey, []string{testParamPrefix})
	v.Set(ratePrefixesKey, []string{testRatePrefix})
	cfg := inboundConfig{
		JWT: jwtConfig{
			Prefixes: []string{testPrefix},
			Param:    paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce},
			Rate:     rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModeEnforce},
		},
		MaxValueLen: 8,
	}

	core, logs := observer.New(zapcore.InfoLevel)
	chain, params, err := NewMiddleware(cfg, v, keys.provider, zap.New(core), testCounter(), testWarningCounter())
	require.NoError(t, err)

	extract := func(*http.Request) (ParamRequest, bool) {
		return ParamRequest{Operation: OperationRead, Names: []string{name}}, true
	}
	mw := chain.Then(params.Middleware(extract)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))

	// a log entry of the given message, which is not the only one logged
	entry := func(message string) map[string]any {
		for _, e := range logs.All() {
			if e.Message == message {
				return e.ContextMap()
			}
		}

		require.Fail(t, "not logged", message)

		return nil
	}

	t.Run("parameter check", func(t *testing.T) {
		bearer := keys.signAs(t, principal, time.Now(), future, map[string]any{
			capabilitiesClaim: []string{testPrefix + "device/.*/config:get", testParamPrefix + "Device.Allowed:read"},
			allowedResources:  partners(testPartner),
		})
		rec := serveRequest(t, mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+bearer) })
		require.Equal(t, http.StatusForbidden, rec.Code)

		got := entry("parameter check: request rejected")
		assert.Equal(t, cut("p"), got["jwt_principal"])
		assert.Equal(t, cut("n"), got["parameter"])
	})

	t.Run("rate limiter", func(t *testing.T) {
		other := strings.Repeat("r", 50)
		bearer := keys.signAs(t, other, time.Now(), future, map[string]any{
			capabilitiesClaim: []string{testPrefix + "device/.*/config:get", testRatePrefix + onePerMinute},
			allowedResources:  partners(testPartner),
		})

		// the second request is over the limit, once the parameter check,
		// which has no capability for this token, lets it through
		var last int
		for range 2 {
			last = serveRequest(t, mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+bearer) }).Code
		}
		require.Equal(t, http.StatusTooManyRequests, last)

		assert.Equal(t, cut("r"), entry("rate limiter: request rejected")["jwt_principal"])
	})
}

func TestNewMiddleware_LimitsMustNotBeNegative(t *testing.T) {
	v := viper.New()
	v.Set(basicKey, []string{encodedCredential("user", "pass")})

	for _, cfg := range []inboundConfig{
		{Basic: []string{encodedCredential("user", "pass")}, MaxValueLen: -1},
		{Basic: []string{encodedCredential("user", "pass")}, MaxWarningLen: -1},
	} {
		_, _, err := NewMiddleware(cfg, v, nil, zap.NewNop(), testCounter(), testWarningCounter())
		assert.Error(t, err)
	}
}

// TestNewMiddleware_ConfiguredLimits checks that the configured length is the
// one used, through the whole chain: a long principal is cut in the metric and
// in the log to the length configured, not the default.
func TestNewMiddleware_ConfiguredLimits(t *testing.T) {
	keys := newTestKeys(t)
	principal := strings.Repeat("p", 50)

	core, logs := observer.New(zapcore.InfoLevel)
	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}}, MaxValueLen: 8}

	checks, warnings := testCounter(), testWarningCounter()
	chain, _, err := NewMiddleware(cfg, v, keys.provider, zap.New(core), checks, warnings)
	require.NoError(t, err)

	mw := chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) }))

	// expired, so it is rejected, and its signature verified, so it is recorded
	bearer := keys.signAs(t, principal, time.Now().Add(-2*time.Hour), time.Now().Add(-time.Hour), map[string]any{allowedResources: partners(testPartner)})
	rec := serveRequest(t, mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+bearer) })
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	cut := strings.Repeat("p", 8) + "..."
	assert.Equal(t, 1, testutil.CollectAndCount(checks))
	assert.Equal(t, float64(1), testutil.ToFloat64(checks.With(prometheus.Labels{
		OutcomeLabel:   Rejected,
		ReasonLabel:    AuthUnsatifiedExp,
		ClientIDLabel:  cut,
		PartnerIDLabel: testPartner,
		EndpointLabel:  NoneEndpoint,
		MethodLabel:    http.MethodGet,
	})))
	assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
}

// TestLongValuesAreBoundedInLogs checks every place a value from a token or a
// request is logged: none is longer than the limit, whatever a caller or an
// issuer sent.
func TestLongValuesAreBoundedInLogs(t *testing.T) {
	long := strings.Repeat("v", 100_000)
	cut := strings.Repeat("v", defaultMaxValueLen) + "..."
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	t.Run("authenticator partner", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		ae := authenticatorEvent{l: zap.New(core)}

		got := ae.getLabels(bascule.AuthenticateEvent[*http.Request]{
			Source:       req,
			InvalidToken: hugeJWT(t, long),
			Err:          basculejwt.ErrExpired,
		})

		assert.Equal(t, long, got[PartnerIDLabel], "the label is bounded by cleanLabels")
		assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
		assert.Equal(t, cut, logField(t, logs, "jwt_partner"))
	})

	t.Run("authorizer rejection partner", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		ae := authorizerEvent{l: zap.New(core)}

		ae.getLabels(bascule.AuthorizeEvent[*http.Request]{Resource: req, Token: hugeJWT(t, long), Err: bascule.ErrUnauthorized})

		assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
		assert.Equal(t, cut, logField(t, logs, "jwt_partner"))
	})

	t.Run("authorizer warning", func(t *testing.T) {
		core, logs := observer.New(zapcore.InfoLevel)
		ae := authorizerEvent{l: zap.New(core), counter: testCounter(), warnings: testWarningCounter()}

		ae.OnEvent(bascule.AuthorizeEvent[*http.Request]{
			Resource: req,
			Token:    hugeJWT(t, long),
			Warnings: []bascule.Warning{{
				Reason: warningWouldReject,
				Attrs:  []bascule.WarningAttr{{Key: KindLabel, Value: "cidr"}, {Key: "cap", Value: long}},
			}},
		})

		warning := logField(t, logs, "warning")
		assert.Len(t, warning, defaultMaxWarningLen+len("..."))
		assert.True(t, strings.HasPrefix(warning, "would-reject; kind=cidr; cap="), warning)
		assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
		assert.Equal(t, cut, logField(t, logs, "jwt_partner"))
	})

	paramLogs := func(t *testing.T, mode string) *observer.ObservedLogs {
		t.Helper()

		core, logs := observer.New(zapcore.DebugLevel)
		check, err := newParamCheck(paramConfig{Prefixes: []string{testParamPrefix}, Mode: mode}, zap.New(core), testCounter(), testWarningCounter(), nil)
		require.NoError(t, err)

		check.record(http.Header{}, req, hugeJWT(t, long), ParamRequest{Operation: OperationRead}, paramDecision{name: long})

		return logs
	}

	t.Run("parameter check enforced", func(t *testing.T) {
		logs := paramLogs(t, ModeEnforce)

		assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
		assert.Equal(t, cut, logField(t, logs, "jwt_partner"))
		assert.Equal(t, cut, logField(t, logs, "parameter"))
	})

	t.Run("parameter check would reject", func(t *testing.T) {
		logs := paramLogs(t, ModePermissive)

		assert.Equal(t, cut, logField(t, logs, "parameter"))
		assert.Len(t, logField(t, logs, "warning"), defaultMaxWarningLen+len("..."))
	})

	t.Run("rate limiter", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		rl, err := newRateLimiter(rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModeEnforce}, zap.New(core), testCounter(), testWarningCounter(), nil)
		require.NoError(t, err)

		rl.record(http.Header{}, req, hugeJWT(t, long), tokenrate.Decision{Reason: tokenrate.RateExceeded})

		assert.Equal(t, cut, logField(t, logs, "jwt_principal"))
		assert.Equal(t, cut, logField(t, logs, "jwt_partner"))
	})
}

// TestAuthorizerEvent_AcceptedIsNotARejection checks that an accepted request
// is counted as accepted and logged as nothing worse than debug.
func TestAuthorizerEvent_AcceptedIsNotARejection(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	counter := testCounter()
	ae := authorizerEvent{l: zap.New(core), counter: counter, warnings: testWarningCounter()}

	ae.OnEvent(bascule.AuthorizeEvent[*http.Request]{
		Resource: httptest.NewRequest(http.MethodGet, "/", nil),
		Token:    testBasicToken{user: "user"},
	})

	assert.Equal(t, 1, testutil.CollectAndCount(counter))
	assert.Equal(t, float64(1), testutil.ToFloat64(counter.With(prometheus.Labels{
		OutcomeLabel: Accepted, ReasonLabel: EmptyLabelValue, ClientIDLabel: "user", PartnerIDLabel: EmptyLabelValue, EndpointLabel: NoneEndpoint, MethodLabel: http.MethodGet,
	})))
	assert.Zero(t, logs.Len(), "an accepted request is not logged as a rejection")
}

// TestNewMiddleware_InvalidUTF8Principal checks that a caller cannot make the
// metrics panic: prometheus refuses a label value that is not valid UTF-8,
// and a basic auth user name is whatever the caller sends.
func TestNewMiddleware_InvalidUTF8Principal(t *testing.T) {
	v := viper.New()
	v.Set(basicKey, []string{encodedCredential("user", "pass")})
	mw, checks, _ := protectCounting(t, inboundConfig{Basic: []string{encodedCredential("user", "pass")}}, v, nil)

	var rec *httptest.ResponseRecorder
	require.NotPanics(t, func() {
		rec = serveRequest(t, mw, func(r *http.Request) {
			r.Header.Set("Authorization", "Basic "+encodedCredential("\xff", "pass"))
		})
	})
	assert.Equal(t, http.StatusUnauthorized, rec.Code)

	assert.Equal(t, float64(1), testutil.ToFloat64(checks.With(prometheus.Labels{
		OutcomeLabel:   Rejected,
		ReasonLabel:    AuthBadCreds,
		ClientIDLabel:  Unauthenticated,
		PartnerIDLabel: EmptyLabelValue,
		EndpointLabel:  NoneEndpoint,
		MethodLabel:    http.MethodGet,
	})))
}

// TestNewMiddleware_RejectedJWTLabels checks, through the whole chain, that a
// JWT turned away for its claims is counted by the client it came from, since
// its signature verified, and that one whose signature did not verify is not.
func TestNewMiddleware_RejectedJWTLabels(t *testing.T) {
	keys := newTestKeys(t)
	other := newTestKeys(t)
	claims := map[string]any{allowedResources: partners(testPartner)}
	past := time.Now().Add(-time.Hour)
	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}}}

	tests := []struct {
		name    string
		bearer  string
		reason  string
		client  string
		partner string
	}{
		{
			name:    "expired, signature verified",
			bearer:  keys.sign(t, testKeyID, past, claims),
			reason:  AuthUnsatifiedExp,
			client:  testPrincipal,
			partner: testPartner,
		}, {
			name:    "expired, signed by an unknown key",
			bearer:  other.sign(t, testKeyID, past, claims),
			reason:  AuthCannotVerify,
			client:  EmptyLabelValue,
			partner: EmptyLabelValue,
		}, {
			name:    "expired, naming an unknown key id",
			bearer:  keys.sign(t, "nope", past, claims),
			reason:  AuthKeyNotFind,
			client:  EmptyLabelValue,
			partner: EmptyLabelValue,
		}, {
			name:    "malformed",
			bearer:  "not.a.jwt",
			reason:  AuthCannotVerify,
			client:  EmptyLabelValue,
			partner: EmptyLabelValue,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mw, checks, _ := protectCounting(t, cfg, v, keys.provider)

			rec := serveRequest(t, mw, func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tc.bearer) })
			assert.Equal(t, http.StatusUnauthorized, rec.Code)

			// counted first, since looking a series up creates it
			assert.Equal(t, 1, testutil.CollectAndCount(checks), "only that series is recorded")
			assert.Equal(t, float64(1), testutil.ToFloat64(checks.With(prometheus.Labels{
				OutcomeLabel:   Rejected,
				ReasonLabel:    tc.reason,
				ClientIDLabel:  tc.client,
				PartnerIDLabel: tc.partner,
				EndpointLabel:  NoneEndpoint,
				MethodLabel:    http.MethodGet,
			})))
		})
	}
}
