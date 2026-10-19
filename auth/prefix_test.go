// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// prefixShapes are capability prefixes of every shape a deployment might
// pick.  The colons in the documented examples are a convention, not syntax:
// a prefix is an arbitrary string cut off the front of the capability, so
// every one of these must behave exactly like the conventional one for every
// kind.  The last two would mean something else as regular expressions, which
// is how bascule once read endpoint and CIDR prefixes; they stay here so that
// never comes back.
var prefixShapes = []struct {
	name   string
	prefix string
}{
	{name: "conventional", prefix: "webpa-prefix-kind:"},
	{name: "colon separated", prefix: "webpa:kind:"},
	{name: "no terminator", prefix: "foo-bar-apple-cider"},
	{name: "with spaces", prefix: "a prefix with spaces "},
	{name: "pipe terminator", prefix: "webpa-prefix-kind|"},
	{name: "regexp metacharacters", prefix: "webpa.prefix+(kind)?*"},
}

// ignoredPrefix is a prefix no kind is configured with, derived from the
// configured one so the two differ only at the front.
func ignoredPrefix(prefix string) string { return "x" + prefix }

func TestPrefixShapes_Endpoint(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	for _, shape := range prefixShapes {
		t.Run(shape.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{shape.prefix})
			cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{shape.prefix}}}
			mw, checks, _ := protectCounting(t, cfg, v, keys.provider)

			token := func(caps ...string) string {
				return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
					capabilitiesClaim: caps,
					allowedResources:  partners(testPartner),
				})
			}

			// the configured prefix is cut and the rest parsed as endpoint:method,
			// whatever the prefix looked like
			rec := serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", token(shape.prefix+"device/.*/config:get"))
			})
			assert.Equal(t, http.StatusOK, rec.Code, "the configured prefix grants")
			assert.True(t, reached(rec))

			// the same capability under an unconfigured prefix grants nothing
			rec = serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", token(ignoredPrefix(shape.prefix)+"device/.*/config:get"))
			})
			assert.Equal(t, http.StatusForbidden, rec.Code, "an unconfigured prefix is ignored")
			assert.False(t, reached(rec))

			rejected := checks.With(prometheus.Labels{
				OutcomeLabel:   Rejected,
				ReasonLabel:    NoCapabilitiesMatch,
				ClientIDLabel:  testPrincipal,
				PartnerIDLabel: testPartner,
				EndpointLabel:  NoneEndpoint,
				MethodLabel:    http.MethodGet,
			})
			assert.Equal(t, float64(1), testutil.ToFloat64(rejected))
		})
	}
}

func TestPrefixShapes_CIDR(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	// httptest.NewRequest sets the peer to 192.0.2.1
	const inside, outside = "192.0.2.0/24", "203.0.113.0/24"

	token := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:get"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	for _, shape := range prefixShapes {
		t.Run(shape.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			v.Set(cidrPrefixesKey, []string{shape.prefix})
			cfg := inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				CIDR:     cidrConfig{Prefixes: []string{shape.prefix}, Mode: ModeEnforce},
			}}
			mw, _, _ := protectCounting(t, cfg, v, keys.provider)

			status := func(authorization string) int {
				rec := serveRequest(t, mw, func(req *http.Request) {
					req.Header.Set("Authorization", authorization)
				})
				assert.Equal(t, rec.Code == http.StatusOK, reached(rec))

				return rec.Code
			}

			assert.Equal(t, http.StatusOK, status(token(shape.prefix+inside)), "origin inside the network")
			assert.Equal(t, http.StatusForbidden, status(token(shape.prefix+outside)), "origin outside the network")
			assert.Equal(t, http.StatusOK, status(token(ignoredPrefix(shape.prefix)+outside)),
				"an unconfigured prefix is ignored, leaving the token unrestricted")
		})
	}
}

func TestPrefixShapes_Rate(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	for _, shape := range prefixShapes {
		t.Run(shape.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			v.Set(ratePrefixesKey, []string{shape.prefix})
			cfg := inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Rate:     rateConfig{Prefixes: []string{shape.prefix}, Mode: ModeEnforce},
			}}
			mw, _, _ := protectCounting(t, cfg, v, keys.provider)

			authorization := "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:get", shape.prefix + onePerMinute},
				allowedResources:  partners(testPartner),
			})
			send := func() *httptest.ResponseRecorder {
				return serveRequest(t, mw, func(req *http.Request) {
					req.Header.Set("Authorization", authorization)
				})
			}

			assert.Equal(t, http.StatusOK, send().Code, "the first call fits 1/1m")

			rec := send()
			assert.Equal(t, http.StatusTooManyRequests, rec.Code, "the second call is over the rate")
			assert.NotEmpty(t, rec.Header().Get("Retry-After"))
		})
	}
}

func TestPrefixShapes_Param(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	for _, shape := range prefixShapes {
		t.Run(shape.name, func(t *testing.T) {
			// the operation suffix keeps its meaning after any prefix is cut
			authorization := "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:all", shape.prefix + deviceInfo + ":read"},
				allowedResources:  partners(testPartner),
			})

			status := func(op Operation) int {
				v := viper.New()
				v.Set(jwtPrefixesKey, []string{testPrefix})
				v.Set(paramPrefixesKey, []string{shape.prefix})
				cfg := inboundConfig{JWT: jwtConfig{
					Prefixes: []string{testPrefix},
					Param:    paramConfig{Prefixes: []string{shape.prefix}, Mode: ModeEnforce},
				}}
				extract := func(*http.Request) (ParamRequest, bool) {
					return ParamRequest{Operation: op, Names: []string{modelName}}, true
				}
				mw, _, _ := protectParams(t, cfg, v, keys.provider, extract)

				rec := serveRequest(t, mw, func(req *http.Request) {
					req.Header.Set("Authorization", authorization)
				})
				assert.Equal(t, rec.Code == http.StatusOK, reached(rec))

				return rec.Code
			}

			assert.Equal(t, http.StatusOK, status(OperationRead), ":read allows reading")
			assert.Equal(t, http.StatusForbidden, status(OperationWrite), ":read does not allow writing")
		})
	}
}

func TestPrefixShapes_ParamMaxAge(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Now()
	stale := now.Add(-2 * time.Hour)

	for _, shape := range prefixShapes {
		t.Run(shape.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			v.Set(paramPrefixesKey, []string{shape.prefix})
			cfg := inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Param: paramConfig{
					Prefixes: []string{shape.prefix},
					Mode:     ModeEnforce,
					MaxAge:   map[string]time.Duration{anything: 30 * time.Minute},
				},
			}}
			mw, _, _ := protectCounting(t, cfg, v, keys.provider)

			cap := shape.prefix + anything
			authorization := "Bearer " + keys.signAs(t, testPrincipal, stale, now.Add(24*time.Hour), map[string]any{
				capabilitiesClaim: []string{testPrefix + "device/.*/config:all", cap},
				allowedResources:  partners(testPartner),
			})
			rec := serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", authorization)
			})

			assert.Equal(t, http.StatusForbidden, rec.Code, "the age limit keys on the pattern after the prefix")
			assert.False(t, reached(rec))

			// the refusal names the capability exactly as the token carries it,
			// quoted when it is not a plain header token
			got := rec.Header().Values(WarningHeader)
			require.Len(t, got, 1, "warnings: %v", got)
			assert.Regexp(t, `cap="?`+regexp.QuoteMeta(cap)+`"?;`, got[0])
		})
	}
}

// TestPrefixOverlap documents the one constraint on prefixes: when one kind's
// prefix is a prefix of another's, a capability of the longer kind is also
// parsed by the shorter.  Here every CIDR capability is also read as an
// endpoint capability.  It grants nothing, since no path matches it, so the
// only cost is noise.
func TestPrefixOverlap(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	const apiPrefix, cidrPrefix = "webpa-prefix-", "webpa-prefix-cidr:"

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{apiPrefix})
	v.Set(cidrPrefixesKey, []string{cidrPrefix})
	cfg := inboundConfig{JWT: jwtConfig{
		Prefixes: []string{apiPrefix},
		CIDR:     cidrConfig{Prefixes: []string{cidrPrefix}, Mode: ModeEnforce},
	}}
	mw, _, _ := protectCounting(t, cfg, v, keys.provider)

	status := func(caps ...string) int {
		rec := serveRequest(t, mw, func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+keys.sign(t, testKeyID, future, map[string]any{
				capabilitiesClaim: caps,
				allowedResources:  partners(testPartner),
			}))
		})
		assert.Equal(t, rec.Code == http.StatusOK, reached(rec))

		return rec.Code
	}

	assert.Equal(t, http.StatusForbidden, status(cidrPrefix+"192.0.2.0/24"),
		"a CIDR capability read as an endpoint capability grants nothing")
	assert.Equal(t, http.StatusOK, status(apiPrefix+"device/.*/config:get", cidrPrefix+"192.0.2.0/24"),
		"beside a real endpoint capability, the CIDR capability still does its own job")
	assert.Equal(t, http.StatusForbidden, status(apiPrefix+"device/.*/config:get", cidrPrefix+"203.0.113.0/24"),
		"and still narrows")
}
