// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/clortho"
	"go.uber.org/zap"
)

const (
	testKeyID      = "test-key"
	testPrefix     = "x1:webpa:api:"
	testCIDRPrefix = "x1:webpa:cidr:"
	testPath       = "/api/v3/device/mac:112233445566/config"
	testPrincipal  = "client0"

	// capabilitiesClaim is the claim basculejwt reads capabilities from.
	capabilitiesClaim = "capabilities"

	// viper keys whose presence selects the inbound mode.
	basicKey        = basicConfigKey
	jwtPrefixesKey  = jwtConfigKey + ".Prefixes"
	cidrPrefixesKey = cidrConfigKey + ".Prefixes"
	cidrModeKey     = cidrConfigKey + ".Mode"
)

// partners is the allowedResources claim stating one partner.
func partners(ids ...string) map[string]any {
	return map[string]any{allowedPartners: ids}
}

// testKeys holds a signing key and a clortho provider that serves its public
// half, so a token can be verified without any network.
type testKeys struct {
	private  *ecdsa.PrivateKey
	provider *clortho.FixedKeyProvider
}

func newTestKeys(t *testing.T) testKeys {
	t.Helper()

	private, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	der, err := x509.MarshalPKIXPublicKey(&private.PublicKey)
	require.NoError(t, err)

	provider, err := clortho.NewFixedKeyProvider(clortho.FixedKeyConfig{
		Keys: []clortho.FixedKey{{
			KeyID: testKeyID,
			Key:   string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
		}},
	})
	require.NoError(t, err)

	return testKeys{private: private, provider: provider}
}

// sign produces a compact JWT naming kid, carrying claims on top of the
// standard ones.  A caller that wants the token unverifiable signs with
// another key.
func (k testKeys) sign(t *testing.T, kid string, exp time.Time, claims map[string]any) string {
	t.Helper()

	builder := jwt.NewBuilder().
		Subject(testPrincipal).
		Issuer("https://issuer.example").
		IssuedAt(time.Now().Add(-time.Minute)).
		Expiration(exp)
	for name, value := range claims {
		builder = builder.Claim(name, value)
	}

	token, err := builder.Build()
	require.NoError(t, err)

	headers := jws.NewHeaders()
	require.NoError(t, headers.Set(jws.KeyIDKey, kid))

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.ES256(), k.private, jws.WithProtectedHeaders(headers)))
	require.NoError(t, err)

	return string(signed)
}

func testCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_auth_capability_check"},
		[]string{OutcomeLabel, ReasonLabel, ClientIDLabel, PartnerIDLabel, EndpointLabel, MethodLabel},
	)
}

func testWarningCounter() *prometheus.CounterVec {
	return prometheus.NewCounterVec(
		prometheus.CounterOpts{Name: "test_auth_capability_warning"},
		[]string{KindLabel, ReasonLabel, ClientIDLabel},
	)
}

func encodedCredential(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// serve runs one request through the chain and reports the status and
// whether the protected handler ran.
func serve(t *testing.T, mw http.Handler, authorization string) (int, bool) {
	t.Helper()

	rec := serveRequest(t, mw, func(req *http.Request) {
		if authorization != "" {
			req.Header.Set("Authorization", authorization)
		}
	})

	return rec.Code, reached(rec)
}

// serveRequest runs one GET of testPath through the chain, after prepare has
// shaped the request.
func serveRequest(t *testing.T, mw http.Handler, prepare func(*http.Request)) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, testPath, nil)
	if prepare != nil {
		prepare(req)
	}

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	return rec
}

// reached reports whether the protected handler ran.
func reached(rec *httptest.ResponseRecorder) bool {
	return rec.Header().Get("X-Reached") == "yes"
}

func protect(t *testing.T, cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider) http.Handler {
	t.Helper()

	mw, _, _ := protectCounting(t, cfg, v, kp)

	return mw
}

// protectCounting is protect, also returning the counters the chain records
// to.
func protectCounting(t *testing.T, cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider) (http.Handler, *prometheus.CounterVec, *prometheus.CounterVec) {
	t.Helper()

	checks, warnings := testCounter(), testWarningCounter()
	chain, err := NewMiddleware(cfg, v, kp, zap.NewNop(), checks, warnings)
	require.NoError(t, err)

	mw := chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reached", "yes")
		w.WriteHeader(http.StatusOK)
	}))

	return mw, checks, warnings
}

func TestNewMiddleware_JWT(t *testing.T) {
	keys := newTestKeys(t)
	other := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	granting := map[string]any{
		capabilitiesClaim: []string{testPrefix + "device/.*/config:get"},
		allowedResources:  partners("comcast"),
	}

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}}}
	mw := protect(t, cfg, v, keys.provider)

	cases := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:          "valid token with capability and partners",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, granting),
			wantStatus:    http.StatusOK,
		}, {
			name: "all-method capability",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:all"},
				allowedResources:  partners("comcast"),
			}),
			wantStatus: http.StatusOK,
		}, {
			name:       "no credentials",
			wantStatus: http.StatusUnauthorized,
		}, {
			name:          "basic credentials are not offered in jwt mode",
			authorization: "Basic " + encodedCredential("user", "pass"),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "token signed by an unknown key",
			authorization: "Bearer " + other.sign(t, testKeyID, future, granting),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "token naming an unknown key id",
			authorization: "Bearer " + keys.sign(t, "nope", future, granting),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "expired token",
			authorization: "Bearer " + keys.sign(t, testKeyID, time.Now().Add(-time.Hour), granting),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "malformed token",
			authorization: "Bearer not.a.jwt",
			wantStatus:    http.StatusUnauthorized,
		}, {
			name: "capability for another method",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:patch"},
				allowedResources:  partners("comcast"),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "capability for another endpoint",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/stat:get"},
				allowedResources:  partners("comcast"),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "capability with another prefix",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{"other:prefix:device/.*/config:get"},
				allowedResources:  partners("comcast"),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "no capabilities",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				allowedResources: partners("comcast"),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "no partner ids",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:get"},
			}),
			wantStatus: http.StatusUnauthorized,
		}, {
			name: "empty partner ids",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:get"},
				allowedResources:  partners(),
			}),
			wantStatus: http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reached := serve(t, mw, tc.authorization)
			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached)
		})
	}
}

func TestNewMiddleware_Basic(t *testing.T) {
	v := viper.New()
	v.Set(basicKey, []string{encodedCredential("user", "pass")})
	cfg := inboundConfig{Basic: []string{encodedCredential("user", "pass"), ""}}
	mw := protect(t, cfg, v, nil)

	cases := []struct {
		name          string
		authorization string
		wantStatus    int
	}{
		{
			name:          "correct credentials",
			authorization: "Basic " + encodedCredential("user", "pass"),
			wantStatus:    http.StatusOK,
		}, {
			name:          "wrong password",
			authorization: "Basic " + encodedCredential("user", "wrong"),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "empty password",
			authorization: "Basic " + encodedCredential("user", ""),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:          "unknown user",
			authorization: "Basic " + encodedCredential("nobody", "pass"),
			wantStatus:    http.StatusUnauthorized,
		}, {
			name:       "no credentials",
			wantStatus: http.StatusUnauthorized,
		}, {
			name:          "bearer is not offered in basic mode",
			authorization: "Bearer anything",
			wantStatus:    http.StatusUnauthorized,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, reached := serve(t, mw, tc.authorization)
			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached)
		})
	}
}

func TestNewMiddleware_Config(t *testing.T) {
	keys := newTestKeys(t)

	cases := []struct {
		name  string
		set   map[string]any
		cfg   inboundConfig
		kp    jws.KeyProvider
		valid bool
	}{
		{
			name:  "basic",
			set:   map[string]any{basicKey: []string{"x"}},
			cfg:   inboundConfig{Basic: []string{encodedCredential("user", "pass")}},
			valid: true,
		}, {
			name:  "jwt",
			set:   map[string]any{jwtPrefixesKey: []string{testPrefix}},
			cfg:   inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, AcceptAllMethod: "any", CacheSize: 10}},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "jwt and basic",
			set: map[string]any{
				basicKey:       []string{"x"},
				jwtPrefixesKey: []string{testPrefix},
			},
			cfg: inboundConfig{Basic: []string{encodedCredential("user", "pass")}},
			kp:  keys.provider,
		}, {
			name: "neither",
			cfg:  inboundConfig{},
		}, {
			name: "jwt with cidr and trusted proxies",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrPrefixesKey: []string{testCIDRPrefix}},
			cfg: inboundConfig{
				JWT: jwtConfig{
					Prefixes: []string{testPrefix},
					CIDR: cidrConfig{
						Prefixes:       []string{testCIDRPrefix},
						Required:       true,
						Mode:           "Permissive",
						CacheSize:      10,
						TrustedProxies: []string{"10.0.0.0/8", " 2001:db8::/32 ", ""},
					},
				},
			},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "cidr without prefixes",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrModeKey: ModeEnforce},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: cidrConfig{Mode: ModeEnforce}}},
			kp:   keys.provider,
		}, {
			name: "cidr with an unknown mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrPrefixesKey: []string{testCIDRPrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: "audit"}}},
			kp:   keys.provider,
		}, {
			name: "cidr with a bad prefix regex",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrPrefixesKey: []string{"("}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: cidrConfig{Prefixes: []string{"("}}}},
			kp:   keys.provider,
		}, {
			name: "trusted proxy is a bare address",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrPrefixesKey: []string{testCIDRPrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				CIDR:     cidrConfig{Prefixes: []string{testCIDRPrefix}, TrustedProxies: []string{"10.0.0.1"}},
			}},
			kp: keys.provider,
		}, {
			name: "jwt without a key provider",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}}},
		}, {
			name: "basic credential is not base64",
			set:  map[string]any{basicKey: []string{"x"}},
			cfg:  inboundConfig{Basic: []string{"!!!not base64!!!"}},
		}, {
			name: "basic credential has no colon",
			set:  map[string]any{basicKey: []string{"x"}},
			cfg:  inboundConfig{Basic: []string{base64.StdEncoding.EncodeToString([]byte("nocolon"))}},
		}, {
			name: "basic credential has no user",
			set:  map[string]any{basicKey: []string{"x"}},
			cfg:  inboundConfig{Basic: []string{encodedCredential("", "pass")}},
		}, {
			name: "basic credentials all blank",
			set:  map[string]any{basicKey: []string{"x"}},
			cfg:  inboundConfig{Basic: []string{"", "  "}},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			for k, val := range tc.set {
				v.Set(k, val)
			}

			_, err := NewMiddleware(tc.cfg, v, tc.kp, zap.NewNop(), testCounter(), testWarningCounter())
			if tc.valid {
				assert.NoError(t, err)
			} else {
				assert.Error(t, err)
			}
		})
	}
}

func TestDecodeBasicCredentials_DoesNotQuoteTheCredential(t *testing.T) {
	const malformed = "!!!not base64 but still private!!!"

	_, err := decodeBasicCredentials([]string{malformed})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), malformed)
}

func TestNewMiddleware_CIDR(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	const (
		peer          = "192.0.2.1:1234" // what httptest.NewRequest sets
		proxy         = "10.0.0.1:443"
		proxyNetwork  = "10.0.0.0/8"
		forwarded     = "198.51.100.7" // the client a proxy reports
		xForwardedFor = "X-Forwarded-For"
	)

	// token signs a token granting the endpoint, with the given further
	// capabilities.
	token := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:get"}, caps...),
			allowedResources:  partners("comcast"),
		})
	}

	permissive := func(required bool) cidrConfig {
		return cidrConfig{Prefixes: []string{testCIDRPrefix}, Required: required, Mode: ModePermissive}
	}
	enforcing := func(required bool) cidrConfig {
		return cidrConfig{Prefixes: []string{testCIDRPrefix}, Required: required}
	}

	cases := []struct {
		name           string
		cidr           cidrConfig
		cidrConfigured bool // whether the cidr section is present at all
		trustedProxies []string
		remoteAddr     string
		headers        map[string]string
		authorization  string
		wantStatus     int
		wantReason     string   // the rejection reason label, for a 403
		wantWarnings   []string // substrings, one per expected warning header
	}{
		{
			name:          "cidr not configured: cidr capabilities are ignored",
			authorization: token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:    http.StatusOK,
		}, {
			name:           "correct if present: no cidr capability is unrestricted",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "correct if present: origin inside",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "correct if present: origin outside",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "correct if present: origin inside one of several",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix+"203.0.113.0/24", testCIDRPrefix+"192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "required: no cidr capability",
			cidrConfigured: true,
			cidr:           enforcing(true),
			authorization:  token(),
			wantStatus:     http.StatusForbidden,
			wantReason:     NoCIDRCapability,
		}, {
			name:           "required: origin inside",
			cidrConfigured: true,
			cidr:           enforcing(true),
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive required: no cidr capability warns",
			cidrConfigured: true,
			cidr:           permissive(true),
			authorization:  token(),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=no-cidr-capability"},
		}, {
			name:           "permissive correct if present: no cidr capability is silent",
			cidrConfigured: true,
			cidr:           permissive(false),
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive correct if present: origin outside warns",
			cidrConfigured: true,
			cidr:           permissive(false),
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=origin-not-allowed; origin=192.0.2.1"},
		}, {
			name:           "only a malformed capability is not unrestricted",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix + "192.0.2.0/33"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
			wantWarnings:   []string{`malformed; kind=cidr; cap="x1:webpa:cidr:192.0.2.0/33"`},
		}, {
			name:           "a bare address is malformed",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix + "192.0.2.1"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
			wantWarnings:   []string{"malformed; kind=cidr"},
		}, {
			name:           "a malformed capability beside a matching one warns on success",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix+"192.0.2.0/33", testCIDRPrefix+"192.0.2.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"malformed; kind=cidr"},
		}, {
			name:           "trusted proxy: x-forwarded-for gives the origin",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded + ", 10.0.0.2"},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "trusted proxy: forwarded gives the origin",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{"Forwarded": `for="` + forwarded + `:4711";proto=https`},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "trusted proxy: forwarded origin outside",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "trusted proxy: the proxy's own address is not the origin",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "untrusted peer: forwarding headers are ignored",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     peer,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "trusted proxy forwarding nothing: origin unknown",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusForbidden,
			wantReason:     UnknownOrigin,
		}, {
			name:           "trusted proxy forwarding nothing: unrestricted token is still fine",
			cidrConfigured: true,
			cidr:           enforcing(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive: unknown origin warns",
			cidrConfigured: true,
			cidr:           permissive(false),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=unknown-origin"},
		}, {
			name:           "ipv6 origin",
			cidrConfigured: true,
			cidr:           enforcing(false),
			remoteAddr:     "[2001:db8::1]:443",
			authorization:  token(testCIDRPrefix + "2001:db8::/32"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "ipv4-mapped ipv6 origin matches an ipv4 network",
			cidrConfigured: true,
			cidr:           enforcing(false),
			remoteAddr:     "[::ffff:192.0.2.1]:443",
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "ipv4 origin does not match an ipv6 network",
			cidrConfigured: true,
			cidr:           enforcing(false),
			authorization:  token(testCIDRPrefix + "2001:db8::/32"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			if tc.cidrConfigured {
				v.Set(cidrPrefixesKey, tc.cidr.Prefixes)
			}

			tc.cidr.TrustedProxies = tc.trustedProxies
			cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: tc.cidr}}
			mw, checks, warnings := protectCounting(t, cfg, v, keys.provider)

			rec := serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", tc.authorization)
				if tc.remoteAddr != "" {
					req.RemoteAddr = tc.remoteAddr
				}
				for name, value := range tc.headers {
					req.Header.Set(name, value)
				}
			})

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached(rec))

			got := rec.Header().Values(WarningHeader)
			require.Len(t, got, len(tc.wantWarnings), "warnings: %v", got)
			for i, want := range tc.wantWarnings {
				assert.True(t, strings.HasPrefix(got[i], want), "warning %q should start with %q", got[i], want)
			}

			// every warning header is also counted, under its own reason
			assert.Equal(t, len(tc.wantWarnings), testutil.CollectAndCount(warnings))

			if tc.wantStatus == http.StatusForbidden {
				rejected := checks.With(prometheus.Labels{
					OutcomeLabel:   Rejected,
					ReasonLabel:    tc.wantReason,
					ClientIDLabel:  testPrincipal,
					PartnerIDLabel: "comcast",
					EndpointLabel:  NoneEndpoint,
					MethodLabel:    http.MethodGet,
				})
				assert.Equal(t, 1.0, testutil.ToFloat64(rejected))
			}
		})
	}
}

func TestNewMiddleware_CIDR_WarningLabels(t *testing.T) {
	keys := newTestKeys(t)

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	v.Set(cidrPrefixesKey, []string{testCIDRPrefix})
	cfg := inboundConfig{JWT: jwtConfig{
		Prefixes: []string{testPrefix},
		CIDR:     cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: ModePermissive},
	}}
	mw, _, warnings := protectCounting(t, cfg, v, keys.provider)

	// one malformed capability and one that does not match: two warnings
	rec := serveRequest(t, mw, func(req *http.Request) {
		req.Header.Set("Authorization", "Bearer "+keys.sign(t, testKeyID, time.Now().Add(time.Hour), map[string]any{
			capabilitiesClaim: []string{
				testPrefix + "device/.*/config:get",
				testCIDRPrefix + "not a network",
				testCIDRPrefix + "203.0.113.0/24",
			},
			allowedResources: partners("comcast"),
		}))
	})
	require.Equal(t, http.StatusOK, rec.Code)

	for _, reason := range []string{"malformed", "origin_not_allowed"} {
		counter := warnings.With(prometheus.Labels{KindLabel: "cidr", ReasonLabel: reason, ClientIDLabel: testPrincipal})
		assert.Equal(t, 1.0, testutil.ToFloat64(counter), reason)
	}
}

// The cidr and trusted proxy keys are documented in tr1d1um.yaml; this checks
// that they land on the right fields.
func TestInboundConfigFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    jwt:
      prefixes: ["x1:webpa:api:"]
      cidr:
        prefixes: ["x1:webpa:cidr:"]
        required: true
        mode: permissive
        cacheSize: 256
        trustedProxies: ["10.0.0.0/8", "2001:db8::/32"]
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg inboundConfig
	require.NoError(t, v.UnmarshalKey(inboundConfigKey, &cfg))

	assert.True(t, v.IsSet(cidrConfigKey))
	assert.Equal(t, []string{"10.0.0.0/8", "2001:db8::/32"}, cfg.JWT.CIDR.TrustedProxies)
	assert.Equal(t, []string{"x1:webpa:cidr:"}, cfg.JWT.CIDR.Prefixes)
	assert.True(t, cfg.JWT.CIDR.Required)
	assert.Equal(t, ModePermissive, cfg.JWT.CIDR.Mode)
	assert.Equal(t, 256, cfg.JWT.CIDR.CacheSize)
}
