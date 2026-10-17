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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
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
	testPartner    = "comcast"

	// capabilitiesClaim is the claim basculejwt reads capabilities from.
	capabilitiesClaim = "capabilities"

	// unknownMode is a mode no check accepts.
	unknownMode = "audit"

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

// sign produces a compact JWT for testPrincipal naming kid, carrying claims
// on top of the standard ones.  A caller that wants the token unverifiable
// signs with another key.
func (k testKeys) sign(t *testing.T, kid string, exp time.Time, claims map[string]any) string {
	t.Helper()

	return k.signAs(t, testPrincipal, exp, claims, kid)
}

// signAs is sign for any principal.  kid defaults to testKeyID.
func (k testKeys) signAs(t *testing.T, principal string, exp time.Time, claims map[string]any, kid ...string) string {
	t.Helper()

	keyID := testKeyID
	if len(kid) > 0 {
		keyID = kid[0]
	}

	builder := jwt.NewBuilder().
		Subject(principal).
		Issuer("https://issuer.example").
		IssuedAt(time.Now().Add(-time.Minute)).
		Expiration(exp)
	for name, value := range claims {
		builder = builder.Claim(name, value)
	}

	token, err := builder.Build()
	require.NoError(t, err)

	headers := jws.NewHeaders()
	require.NoError(t, headers.Set(jws.KeyIDKey, keyID))

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
	chain, _, err := NewMiddleware(cfg, v, kp, zap.NewNop(), checks, warnings)
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
		allowedResources:  partners(testPartner),
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
				allowedResources:  partners(testPartner),
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
				allowedResources:  partners(testPartner),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "capability for another endpoint",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/stat:get"},
				allowedResources:  partners(testPartner),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "capability with another prefix",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{"other:prefix:device/.*/config:get"},
				allowedResources:  partners(testPartner),
			}),
			wantStatus: http.StatusForbidden,
		}, {
			name: "no capabilities",
			authorization: "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				allowedResources: partners(testPartner),
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
						Mode:           "PermissiveRequired",
						CacheSize:      10,
						TrustedProxies: []string{"10.0.0.0/8", " 2001:db8::/32 ", ""},
					},
				},
			},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "jwt with rate",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate: rateConfig{
					Prefixes:  []string{testRatePrefix},
					Mode:      "PermissiveWithWarnings",
					Overrides: map[string][]string{"a": {"50/1m", " 100/24h "}, "b": {}},
				},
			}},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "rate without prefixes",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, rateModeKey: ModeEnforce},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: rateConfig{Mode: ModeEnforce}}},
			kp:   keys.provider,
		}, {
			name: "rate with an empty prefix",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{""}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: rateConfig{Prefixes: []string{""}}}},
			kp:   keys.provider,
		}, {
			name: "rate with an unknown mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: rateConfig{Prefixes: []string{testRatePrefix}, Mode: unknownMode}}},
			kp:   keys.provider,
		}, {
			name: "jwt with rate defaults",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate:     rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{"400/1m", " 50000/24h "}},
			}},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "jwt with param",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramPrefixesKey: []string{testParamPrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Param: paramConfig{
					Prefixes:  []string{testParamPrefix},
					Mode:      "Enforce",
					Overrides: map[string][]string{"a": {"Device.**:read", " Device.WiFi.** "}, "b": {}},
				},
			}},
			kp:    keys.provider,
			valid: true,
		}, {
			name: "param without prefixes",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramConfigKey + ".Mode": ModeEnforce},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: paramConfig{Mode: ModeEnforce}}},
			kp:   keys.provider,
		}, {
			name: "param with an empty prefix",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramPrefixesKey: []string{""}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: paramConfig{Prefixes: []string{""}}}},
			kp:   keys.provider,
		}, {
			name: "param with an unknown mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramPrefixesKey: []string{testParamPrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: paramConfig{Prefixes: []string{testParamPrefix}, Mode: unknownMode}}},
			kp:   keys.provider,
		}, {
			name: "param has no required mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramPrefixesKey: []string{testParamPrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeRequired}}},
			kp:   keys.provider,
		}, {
			name: "param override is malformed",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, paramPrefixesKey: []string{testParamPrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Param:    paramConfig{Prefixes: []string{testParamPrefix}, Overrides: map[string][]string{"a": {"Device.**:wirte"}}},
			}},
			kp: keys.provider,
		}, {
			name: "rate has no required mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModeRequired}}},
			kp:   keys.provider,
		}, {
			name: "rate has no permissive required mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissiveRequired}}},
			kp:   keys.provider,
		}, {
			name: "rate default is malformed",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate:     rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{"600/1d"}},
			}},
			kp: keys.provider,
		}, {
			name: "rate window bounds are inverted",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate:     rateConfig{Prefixes: []string{testRatePrefix}, MinWindow: time.Hour, MaxWindow: time.Minute},
			}},
			kp: keys.provider,
		}, {
			name: "rate override is malformed",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, ratePrefixesKey: []string{testRatePrefix}},
			cfg: inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate:     rateConfig{Prefixes: []string{testRatePrefix}, Overrides: map[string][]string{"a": {"50/1d"}}},
			}},
			kp: keys.provider,
		}, {
			name: "cidr without prefixes",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrModeKey: ModeEnforce},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: cidrConfig{Mode: ModeEnforce}}},
			kp:   keys.provider,
		}, {
			name: "cidr with an unknown mode",
			set:  map[string]any{jwtPrefixesKey: []string{testPrefix}, cidrPrefixesKey: []string{testCIDRPrefix}},
			cfg:  inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, CIDR: cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: unknownMode}}},
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

			_, _, err := NewMiddleware(tc.cfg, v, tc.kp, zap.NewNop(), testCounter(), testWarningCounter())
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
			allowedResources:  partners(testPartner),
		})
	}

	withMode := func(mode string) cidrConfig {
		return cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: mode}
	}

	// malformed is how every malformed cidr warning starts
	const malformed = "malformed; kind=cidr"

	cases := []struct {
		name           string
		cidr           cidrConfig
		cidrConfigured bool // whether the cidr section is present at all
		silent         bool // the mode keeps wantWarnings from the caller; they are only counted
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
			cidr:           withMode(ModeEnforce),
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "correct if present: origin inside",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "correct if present: origin outside",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "correct if present: origin inside one of several",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix+"203.0.113.0/24", testCIDRPrefix+"192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "required: no cidr capability",
			cidrConfigured: true,
			cidr:           withMode(ModeRequired),
			authorization:  token(),
			wantStatus:     http.StatusForbidden,
			wantReason:     NoCIDRCapability,
		}, {
			name:           "required: origin inside",
			cidrConfigured: true,
			cidr:           withMode(ModeRequired),
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive required with warnings: no cidr capability warns",
			cidrConfigured: true,
			cidr:           withMode(ModePermissiveRequiredWithWarnings),
			authorization:  token(),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=no-cidr-capability"},
		}, {
			name:           "permissive correct if present: no cidr capability is silent",
			cidrConfigured: true,
			cidr:           withMode(ModePermissive),
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive with warnings: origin outside warns",
			cidrConfigured: true,
			cidr:           withMode(ModePermissiveWithWarnings),
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=origin-not-allowed; origin=192.0.2.1"},
		}, {
			name:           "permissive: origin outside is counted but the caller is not told",
			cidrConfigured: true,
			cidr:           withMode(ModePermissive),
			silent:         true,
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=origin-not-allowed"},
		}, {
			name:           "permissive: a malformed capability is counted but the caller is not told",
			cidrConfigured: true,
			cidr:           withMode(ModePermissive),
			silent:         true,
			authorization:  token(testCIDRPrefix+"192.0.2.0/33", testCIDRPrefix+"192.0.2.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{malformed},
		}, {
			name:           "the default mode is permissive: origin outside is only counted",
			cidrConfigured: true,
			cidr:           cidrConfig{Prefixes: []string{testCIDRPrefix}},
			silent:         true,
			authorization:  token(testCIDRPrefix + "203.0.113.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=origin-not-allowed"},
		}, {
			name:           "permissive required: no cidr capability is counted but the caller is not told",
			cidrConfigured: true,
			cidr:           withMode(ModePermissiveRequired),
			silent:         true,
			authorization:  token(),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=no-cidr-capability"},
		}, {
			name:           "only a malformed capability is not unrestricted",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix + "192.0.2.0/33"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
			wantWarnings:   []string{`malformed; kind=cidr; cap="x1:webpa:cidr:192.0.2.0/33"`},
		}, {
			name:           "a bare address is malformed",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix + "192.0.2.1"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
			wantWarnings:   []string{malformed},
		}, {
			name:           "a malformed capability beside a matching one warns on success",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			authorization:  token(testCIDRPrefix+"192.0.2.0/33", testCIDRPrefix+"192.0.2.0/24"),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{malformed},
		}, {
			name:           "trusted proxy: x-forwarded-for gives the origin",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded + ", 10.0.0.2"},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "trusted proxy: forwarded gives the origin",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{"Forwarded": `for="` + forwarded + `:4711";proto=https`},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "trusted proxy: forwarded origin outside",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "trusted proxy: the proxy's own address is not the origin",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "untrusted peer: forwarding headers are ignored",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     peer,
			headers:        map[string]string{xForwardedFor: forwarded},
			authorization:  token(testCIDRPrefix + "198.51.100.0/24"),
			wantStatus:     http.StatusForbidden,
			wantReason:     OriginNotAllowed,
		}, {
			name:           "trusted proxy forwarding nothing: origin unknown",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusForbidden,
			wantReason:     UnknownOrigin,
		}, {
			name:           "trusted proxy forwarding nothing: unrestricted token is still fine",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(),
			wantStatus:     http.StatusOK,
		}, {
			name:           "permissive with warnings: unknown origin warns",
			cidrConfigured: true,
			cidr:           withMode(ModePermissiveWithWarnings),
			trustedProxies: []string{proxyNetwork},
			remoteAddr:     proxy,
			authorization:  token(testCIDRPrefix + proxyNetwork),
			wantStatus:     http.StatusOK,
			wantWarnings:   []string{"would-reject; kind=cidr; reason=unknown-origin"},
		}, {
			name:           "ipv6 origin",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			remoteAddr:     "[2001:db8::1]:443",
			authorization:  token(testCIDRPrefix + "2001:db8::/32"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "ipv4-mapped ipv6 origin matches an ipv4 network",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
			remoteAddr:     "[::ffff:192.0.2.1]:443",
			authorization:  token(testCIDRPrefix + "192.0.2.0/24"),
			wantStatus:     http.StatusOK,
		}, {
			name:           "ipv4 origin does not match an ipv6 network",
			cidrConfigured: true,
			cidr:           withMode(ModeEnforce),
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
			if tc.silent {
				assert.Empty(t, got, "a silent mode writes no warning header")
			} else {
				require.Len(t, got, len(tc.wantWarnings), "warnings: %v", got)
				for i, want := range tc.wantWarnings {
					assert.True(t, strings.HasPrefix(got[i], want), "warning %q should start with %q", got[i], want)
				}
			}

			// every warning is counted, under its own reason, told or not
			assert.Equal(t, len(tc.wantWarnings), testutil.CollectAndCount(warnings))

			if tc.wantStatus == http.StatusForbidden {
				rejected := checks.With(prometheus.Labels{
					OutcomeLabel:   Rejected,
					ReasonLabel:    tc.wantReason,
					ClientIDLabel:  testPrincipal,
					PartnerIDLabel: testPartner,
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
			allowedResources: partners(testPartner),
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
        mode: permissiveRequiredWithWarnings
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
	assert.Equal(t, ModePermissiveRequiredWithWarnings, cfg.JWT.CIDR.Mode)
	assert.Equal(t, 256, cfg.JWT.CIDR.CacheSize)
}

var allModes = []string{
	ModePermissive, ModePermissiveWithWarnings, ModeEnforce, ModeRequired,
	ModePermissiveRequired, ModePermissiveRequiredWithWarnings,
}

func TestParseMode(t *testing.T) {
	rateModes := []string{ModePermissive, ModePermissiveWithWarnings, ModeEnforce}

	cases := []struct {
		mode     string
		accepted []string
		want     checkMode
		valid    bool
	}{
		{mode: "", accepted: allModes, want: checkMode{permissive: true}, valid: true},
		{mode: "permissive", accepted: allModes, want: checkMode{permissive: true}, valid: true},
		{mode: " Permissive ", accepted: allModes, want: checkMode{permissive: true}, valid: true},
		{mode: "permissiveWithWarnings", accepted: allModes, want: checkMode{permissive: true, warnings: true}, valid: true},
		{mode: "PERMISSIVEWITHWARNINGS", accepted: allModes, want: checkMode{permissive: true, warnings: true}, valid: true},
		{mode: "enforce", accepted: allModes, want: checkMode{warnings: true}, valid: true},
		{mode: "required", accepted: allModes, want: checkMode{warnings: true, required: true}, valid: true},
		{mode: "permissiveRequired", accepted: allModes, want: checkMode{permissive: true, required: true}, valid: true},
		{mode: "permissiveRequiredWithWarnings", accepted: allModes, want: checkMode{permissive: true, warnings: true, required: true}, valid: true},
		{mode: "permissive-required"},
		{mode: unknownMode, accepted: allModes},
		{mode: "true", accepted: allModes},
		{mode: "", accepted: rateModes, want: checkMode{permissive: true}, valid: true},
		{mode: "Enforce", accepted: rateModes, want: checkMode{warnings: true}, valid: true},
		{mode: "required", accepted: rateModes},
		{mode: "permissiveRequired", accepted: rateModes},
		{mode: "permissiveRequiredWithWarnings", accepted: rateModes},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("%q of %d", tc.mode, len(tc.accepted)), func(t *testing.T) {
			got, err := parseMode(tc.mode, "key", tc.accepted...)
			if !tc.valid {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "`key.Mode`")
				for _, a := range tc.accepted {
					assert.Contains(t, err.Error(), strconv.Quote(a), "the error lists the accepted modes")
				}

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestCheckModeString(t *testing.T) {
	for _, mode := range allModes {
		parsed, err := parseMode(mode, "key", allModes...)
		require.NoError(t, err)
		assert.Equal(t, mode, parsed.String())
	}

	assert.Equal(t, "", checkMode{required: true}.String(), "a combination that is not a mode")
}
