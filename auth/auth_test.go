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
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/clortho"
	"go.uber.org/zap"
)

const (
	testKeyID     = "test-key"
	testPrefix    = "x1:webpa:api:"
	testPath      = "/api/v3/device/mac:112233445566/config"
	testPrincipal = "client0"

	// capabilitiesClaim is the claim basculejwt reads capabilities from.
	capabilitiesClaim = "capabilities"

	// viper keys whose presence selects the inbound mode.
	basicKey       = basicConfigKey
	jwtPrefixesKey = jwtConfigKey + ".Prefixes"
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

func encodedCredential(user, password string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + password))
}

// serve runs one request through the chain and reports the status and
// whether the protected handler ran.
func serve(t *testing.T, mw http.Handler, authorization string) (int, bool) {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, testPath, nil)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	rec := httptest.NewRecorder()
	mw.ServeHTTP(rec, req)

	return rec.Code, rec.Header().Get("X-Reached") == "yes"
}

func protect(t *testing.T, cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider) http.Handler {
	t.Helper()

	chain, err := NewMiddleware(cfg, v, kp, zap.NewNop(), testCounter())
	require.NoError(t, err)

	return chain.Then(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reached", "yes")
		w.WriteHeader(http.StatusOK)
	}))
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

			_, err := NewMiddleware(tc.cfg, v, tc.kp, zap.NewNop(), testCounter())
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
