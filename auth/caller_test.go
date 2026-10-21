// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/sallust"
)

// protectLogging builds the chain around a handler that logs one line with
// the request logger, the way a service handler does, and returns the lines
// logged.  extract, when given, applies the parameter check to the handler.
func protectLogging(
	t *testing.T, cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider,
	extract ParamExtractor,
) (http.Handler, *observer.ObservedLogs) {
	t.Helper()

	core, logs := observer.New(zapcore.DebugLevel)
	chain, params, err := NewMiddleware(cfg, v, kp, zap.New(core),
		testCounter(), testWarningCounter())
	require.NoError(t, err)

	var handler http.Handler = http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			sallust.Get(r.Context()).Info("handled")
			w.Header().Set("X-Reached", "yes")
			w.WriteHeader(http.StatusOK)
		})
	if extract != nil {
		handler = params.Middleware(extract)(handler)
	}

	return chain.Then(handler), logs
}

// handledFields returns the fields of the handler's line, which is the last
// one logged and the only one at info level with that message.
func handledFields(t *testing.T, logs *observer.ObservedLogs) map[string]any {
	t.Helper()

	handled := logs.FilterMessage("handled").All()
	require.Len(t, handled, 1, "the handler should have logged once")

	return handled[0].ContextMap()
}

// stringsField returns a field logged with zap.Strings, which the observer
// reports as a slice of any.
func stringsField(t *testing.T, fields map[string]any, key string) []string {
	t.Helper()

	values, ok := fields[key].([]any)
	require.True(t, ok, "%s should be a list", key)

	texts := make([]string, 0, len(values))
	for _, v := range values {
		texts = append(texts, v.(string))
	}

	return texts
}

func TestLogCaller(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	const (
		peer     = "192.0.2.1:1234" // httptest.NewRequest's RemoteAddr
		peerAddr = "192.0.2.1"
		client   = "198.51.100.7"
		network  = "203.0.113.0/24"
	)

	// bearer signs a token for testPrincipal granting the endpoint, with any
	// further capabilities.
	bearer := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:all"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	jwtOnly := func() (inboundConfig, *viper.Viper) {
		v := viper.New()
		v.Set(jwtPrefixesKey, []string{testPrefix})

		return inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}}}, v
	}

	tests := []struct {
		name    string
		setup   func() (inboundConfig, *viper.Viper)
		kp      jws.KeyProvider
		prepare func(*http.Request)
		// remoteAddr replaces httptest's peer address when set.
		remoteAddr string

		wantPrincipal string
		wantPartner   string
		wantOrigin    string // "" when no origin should be logged
		wantCIDR      []string
		wantRate      []string
	}{
		{
			name:          "jwt with no limits configured",
			setup:         jwtOnly,
			kp:            keys.provider,
			prepare:       func(r *http.Request) { r.Header.Set("Authorization", bearer()) },
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
			wantOrigin:    peerAddr,
		}, {
			name: "jwt held to its token's networks and rates",
			setup: func() (inboundConfig, *viper.Viper) {
				cfg, v := jwtOnly()
				v.Set(cidrPrefixesKey, []string{testCIDRPrefix})
				v.Set(ratePrefixesKey, []string{testRatePrefix})
				cfg.JWT.CIDR = cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: ModePermissive}
				cfg.JWT.Rate = rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissive}

				return cfg, v
			},
			kp: keys.provider,
			prepare: func(r *http.Request) {
				r.Header.Set("Authorization", bearer(testCIDRPrefix+network, testRatePrefix+"50/1m"))
			},
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
			wantOrigin:    peerAddr,
			wantCIDR:      []string{network},
			wantRate:      []string{"50/1m"},
		}, {
			name: "jwt held to its overrides, calling through a trusted proxy",
			setup: func() (inboundConfig, *viper.Viper) {
				cfg, v := jwtOnly()
				v.Set(cidrPrefixesKey, []string{testCIDRPrefix})
				v.Set(ratePrefixesKey, []string{testRatePrefix})
				cfg.JWT.CIDR = cidrConfig{
					Prefixes:       []string{testCIDRPrefix},
					Mode:           ModePermissive,
					TrustedProxies: []string{"192.0.2.0/24"},
					Overrides:      map[string][]string{testPrincipal: {"10.0.0.0/8", "2001:db8::/32"}},
				}
				cfg.JWT.Rate = rateConfig{
					Prefixes:  []string{testRatePrefix},
					Mode:      ModePermissive,
					Overrides: map[string][]string{testPrincipal: {"1000/24h", "10/1m"}},
				}

				return cfg, v
			},
			kp: keys.provider,
			prepare: func(r *http.Request) {
				// the token's own networks and rates are replaced
				r.Header.Set("Authorization", bearer(testCIDRPrefix+network, testRatePrefix+"50/1m"))
				r.Header.Set("X-Forwarded-For", client)
			},
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
			wantOrigin:    client,
			wantCIDR:      []string{"10.0.0.0/8", "2001:db8::/32"},
			// tokenrate reports the rates it applies shortest window first
			wantRate: []string{"10/1m", "1000/24h"},
		}, {
			name: "jwt with no capabilities of the configured kinds is unrestricted",
			setup: func() (inboundConfig, *viper.Viper) {
				cfg, v := jwtOnly()
				v.Set(cidrPrefixesKey, []string{testCIDRPrefix})
				v.Set(ratePrefixesKey, []string{testRatePrefix})
				cfg.JWT.CIDR = cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: ModePermissive}
				cfg.JWT.Rate = rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissive}

				return cfg, v
			},
			kp:            keys.provider,
			prepare:       func(r *http.Request) { r.Header.Set("Authorization", bearer()) },
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
			wantOrigin:    peerAddr,
			wantCIDR:      []string{},
			wantRate:      []string{},
		}, {
			name: "forwarding headers from an untrusted peer are ignored",
			setup: func() (inboundConfig, *viper.Viper) {
				cfg, v := jwtOnly()
				v.Set(cidrPrefixesKey, []string{testCIDRPrefix})
				cfg.JWT.CIDR = cidrConfig{Prefixes: []string{testCIDRPrefix}, Mode: ModePermissive}

				return cfg, v
			},
			kp: keys.provider,
			prepare: func(r *http.Request) {
				r.Header.Set("Authorization", bearer())
				r.Header.Set("X-Forwarded-For", client)
			},
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
			wantOrigin:    peerAddr,
			wantCIDR:      []string{},
		}, {
			name: "basic credentials have a principal and no partner",
			setup: func() (inboundConfig, *viper.Viper) {
				v := viper.New()
				v.Set(basicKey, []string{encodedCredential("user", "pass")})

				return inboundConfig{Basic: []string{encodedCredential("user", "pass")}}, v
			},
			prepare: func(r *http.Request) {
				r.Header.Set("Authorization", "Basic "+encodedCredential("user", "pass"))
			},
			wantPrincipal: "user",
			wantOrigin:    peerAddr,
		}, {
			name:  "an unparsable peer address has no origin",
			setup: jwtOnly,
			kp:    keys.provider,
			prepare: func(r *http.Request) {
				r.Header.Set("Authorization", bearer())
				r.RemoteAddr = "not an address"
			},
			remoteAddr:    "not an address",
			wantPrincipal: testPrincipal,
			wantPartner:   testPartner,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, v := tc.setup()
			mw, logs := protectLogging(t, cfg, v, tc.kp, nil)

			rec := serveRequest(t, mw, tc.prepare)
			require.Equal(t, http.StatusOK, rec.Code)
			require.True(t, reached(rec))

			fields := handledFields(t, logs)
			assert.Equal(t, tc.wantPrincipal, fields[PrincipalField])
			assert.Equal(t, tc.wantPartner, fields[PartnerField])
			assert.Contains(t, fields, "requestURL", "the request fields set before auth are kept")

			remoteAddr := peer
			if tc.remoteAddr != "" {
				remoteAddr = tc.remoteAddr
			}
			assert.Equal(t, remoteAddr, fields[RemoteAddrField])

			if tc.wantOrigin == "" {
				assert.NotContains(t, fields, OriginField)
			} else {
				assert.Equal(t, tc.wantOrigin, fields[OriginField])
			}

			if tc.wantCIDR == nil {
				assert.NotContains(t, fields, CIDRField, "the cidr check is not configured")
			} else {
				assert.Equal(t, tc.wantCIDR, stringsField(t, fields, CIDRField))
			}

			if tc.wantRate == nil {
				assert.NotContains(t, fields, RateField, "the rate check is not configured")
			} else {
				assert.Equal(t, tc.wantRate, stringsField(t, fields, RateField))
			}

			assert.NotContains(t, fields, ParamField, "no parameter check on this route")

			// the authenticator's line names the caller and the request
			// too, though it is written before the caller is accepted
			authenticated := logs.FilterMessage("authenticator event: creds authenticated").All()
			require.Len(t, authenticated, 1)
			assert.Equal(t, zapcore.DebugLevel, authenticated[0].Level)
			assert.Equal(t, tc.wantPrincipal, authenticated[0].ContextMap()[PrincipalField])
			assert.Equal(t, tc.wantPartner, authenticated[0].ContextMap()[PartnerField])
			assert.Contains(t, authenticated[0].ContextMap(), "requestURL")
		})
	}
}

// TestRequestLogger checks that a listener's line is written with the
// request's logger when there is one, and the listener's own otherwise.
func TestRequestLogger(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	own := zap.New(core).With(zap.String("logger", "own"))
	theirs := zap.New(core).With(zap.String("logger", "request"))
	token := testClaimsToken(t, map[string]any{allowedResources: partners(testPartner)})

	ae := authenticatorEvent{l: own}

	t.Run("with a request logger", func(t *testing.T) {
		logs.TakeAll()

		req := httptest.NewRequest(http.MethodGet, testPath, nil)
		req = req.WithContext(sallust.With(req.Context(), theirs))
		ae.OnEvent(bascule.AuthenticateEvent[*http.Request]{Source: req, Token: token})

		entries := logs.All()
		require.Len(t, entries, 1)
		fields := entries[0].ContextMap()
		assert.Equal(t, "request", fields["logger"])
		assert.Equal(t, "client", fields[PrincipalField])
		assert.Equal(t, testPartner, fields[PartnerField])
	})

	t.Run("without a request logger", func(t *testing.T) {
		logs.TakeAll()

		req := httptest.NewRequest(http.MethodGet, testPath, nil)
		ae.OnEvent(bascule.AuthenticateEvent[*http.Request]{Source: req, Token: token})

		entries := logs.All()
		require.Len(t, entries, 1)
		assert.Equal(t, "own", entries[0].ContextMap()["logger"])
	})

	t.Run("without a request", func(t *testing.T) {
		logs.TakeAll()

		ae.OnEvent(bascule.AuthenticateEvent[*http.Request]{Token: token})

		entries := logs.All()
		require.Len(t, entries, 1)
		assert.Equal(t, "own", entries[0].ContextMap()["logger"])
	})
}

// TestLogCaller_BoundsValues checks that the principal, which comes from the
// token, is cut like every other recorded value.
func TestLogCaller_BoundsValues(t *testing.T) {
	keys := newTestKeys(t)
	long := strings.Repeat("p", 100)
	now := time.Now()
	token := keys.signAs(t, long, now.Add(-time.Minute), now.Add(time.Hour),
		map[string]any{
			capabilitiesClaim: []string{testPrefix + "device/.*/config:all"},
			allowedResources:  partners(testPartner),
		})

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	cfg := inboundConfig{MaxValueLen: 16, JWT: jwtConfig{Prefixes: []string{testPrefix}}}
	mw, logs := protectLogging(t, cfg, v, keys.provider, nil)

	rec := serveRequest(t, mw, func(r *http.Request) {
		r.Header.Set("Authorization", "Bearer "+token)
	})
	require.Equal(t, http.StatusOK, rec.Code)

	fields := handledFields(t, logs)
	assert.Equal(t, long[:16]+"...", fields[PrincipalField])
}

func TestLogCaller_Params(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	bearer := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:all"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	read := func(names ...string) ParamExtractor {
		return func(*http.Request) (ParamRequest, bool) {
			return ParamRequest{Operation: OperationRead, Names: names}, true
		}
	}
	nothing := func(*http.Request) (ParamRequest, bool) { return ParamRequest{}, false }

	tests := []struct {
		name      string
		overrides map[string][]string
		caps      []string
		extract   ParamExtractor
		want      []string // nil when the field should be absent
	}{
		{
			name: "held to the token's capabilities, malformed ones aside",
			caps: []string{
				testParamPrefix + "Device.DeviceInfo.**:read",
				testParamPrefix + "Device.WiFi.**:wirte",
				testParamPrefix + "Device.Time.*",
			},
			extract: read("Device.DeviceInfo.SerialNumber"),
			want:    []string{"Device.DeviceInfo.**:read", "Device.Time.*"},
		}, {
			name:      "held to an override",
			overrides: map[string][]string{testPrincipal: {" Device.DeviceInfo.**:read "}},
			caps:      []string{testParamPrefix + "Device.**"},
			extract:   read("Device.DeviceInfo.SerialNumber"),
			want:      []string{"Device.DeviceInfo.**:read"},
		}, {
			name:    "unrestricted",
			extract: read("Device.DeviceInfo.SerialNumber"),
			want:    []string{},
		}, {
			name:    "nothing to check on the route",
			caps:    []string{testParamPrefix + "Device.**"},
			extract: nothing,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			v.Set(paramPrefixesKey, []string{testParamPrefix})
			cfg := inboundConfig{JWT: jwtConfig{
				Prefixes: []string{testPrefix},
				Param: paramConfig{
					Prefixes:  []string{testParamPrefix},
					Mode:      ModePermissive,
					Overrides: tc.overrides,
				},
			}}
			mw, logs := protectLogging(t, cfg, v, keys.provider, tc.extract)

			rec := serveRequest(t, mw, func(r *http.Request) {
				r.Header.Set("Authorization", bearer(tc.caps...))
			})
			require.Equal(t, http.StatusOK, rec.Code)

			fields := handledFields(t, logs)
			if tc.want == nil {
				assert.NotContains(t, fields, ParamField)
			} else {
				assert.Equal(t, tc.want, stringsField(t, fields, ParamField))
			}
		})
	}
}

// TestCallerRecord_OutsideTheChain checks that the resolver and the logger
// cope without a record, which is what a context from outside the chain
// gives them.
func TestCallerRecord_OutsideTheChain(t *testing.T) {
	ctx := context.Background()
	require.Nil(t, recordFrom(ctx))

	network := netip.MustParsePrefix("10.0.0.0/8")
	resolve := resolveNetworks(map[string][]netip.Prefix{"a": {network}})

	networks, err := resolve(ctx, "A", nil)
	require.NoError(t, err)
	assert.Equal(t, []netip.Prefix{network}, networks,
		"overrides are matched without regard to case")

	networks, err = resolve(ctx, "b", []netip.Prefix{network})
	require.NoError(t, err)
	assert.Equal(t, []netip.Prefix{network}, networks,
		"a caller without an override keeps the token's networks")

	held, ok := recordFrom(ctx).heldTo()
	assert.False(t, ok)
	assert.Nil(t, held)
}
