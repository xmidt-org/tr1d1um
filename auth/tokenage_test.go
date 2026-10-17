// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	tooOld       = "rejected; kind=param; reason=token-too-old; cap="
	wouldBeOld   = "would-reject; kind=param; reason=token-too-old; cap="
	noIssuedAt   = "rejected; kind=param; reason=no-issued-at; cap="
	wifiSecurity = "Device.WiFi.Security.**"
)

func TestNewMiddleware_ParamMaxAge(t *testing.T) {
	keys := newTestKeys(t)
	now := time.Now()
	future := now.Add(24 * time.Hour)

	// token signs a token issued at iat, granting the endpoint, with the
	// given parameter capabilities.
	token := func(iat time.Time, caps ...string) string {
		return "Bearer " + keys.signAs(t, testPrincipal, iat, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:all"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	fresh, stale := now.Add(-time.Minute), now.Add(-2*time.Hour)
	limits := map[string]time.Duration{anything: 30 * time.Minute, wifiSecurity: 15 * time.Minute}
	enforcing := paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce, MaxAge: limits}

	cases := []struct {
		name          string
		cfg           paramConfig
		silent        bool // the mode keeps wantWarnings from the caller; they are only counted
		authorization string
		wantStatus    int
		wantReason    string   // the rejection reason label, for a 403
		wantWarnings  []string // prefixes, one per expected warning header
	}{
		{
			name:          "no limits: an old token with a broad capability is fine",
			cfg:           paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce},
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusOK,
		}, {
			name:          "a fresh token with a limited capability",
			cfg:           enforcing,
			authorization: token(fresh, testParamPrefix+anything),
			wantStatus:    http.StatusOK,
		}, {
			name:          "an old token with a limited capability is refused",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
			wantWarnings:  []string{tooOld + `"webpa-prefix:param:Device.**"; maxAge=30m0s; age=`},
		}, {
			name:          "the whole token is refused, whatever else it could do",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+deviceInfo+":read", testParamPrefix+wifiSecurity),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
			wantWarnings:  []string{tooOld + `"webpa-prefix:param:Device.WiFi.Security.**"; maxAge=15m0s`},
		}, {
			name:          "an old token with only unlimited capabilities is fine",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+deviceInfo+":read"),
			wantStatus:    http.StatusOK,
		}, {
			name:          "each pattern has its own limit",
			cfg:           enforcing,
			authorization: token(now.Add(-20*time.Minute), testParamPrefix+wifiSecurity),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name:          "a limit without an operation applies to every suffix",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+anything+":write"),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name:          "a limit without an operation applies to a malformed suffix too",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+anything+":wirte"),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name: "a limit with an operation applies only to it",
			cfg: paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce,
				MaxAge: map[string]time.Duration{anything + ":write": 30 * time.Minute}},
			authorization: token(stale, testParamPrefix+anything+":read"),
			wantStatus:    http.StatusOK,
		}, {
			name: "a limit with an operation matches that suffix",
			cfg: paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce,
				MaxAge: map[string]time.Duration{anything + ":write": 30 * time.Minute}},
			authorization: token(stale, testParamPrefix+anything+":write"),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name: "a limit on all matches a capability with no suffix",
			cfg: paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce,
				MaxAge: map[string]time.Duration{anything + ":all": 30 * time.Minute}},
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name:          "patterns match without regard to case",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+"device.**"),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name:          "a different pattern is not the same capability",
			cfg:           enforcing,
			authorization: token(stale, testParamPrefix+"Device.*"),
			wantStatus:    http.StatusOK,
		}, {
			name:          "another parameter prefix is ignored by the age limit",
			cfg:           enforcing,
			authorization: token(stale, "other:param:"+anything),
			wantStatus:    http.StatusOK,
		}, {
			name:          "a token without an issue time cannot carry a limited capability",
			cfg:           enforcing,
			authorization: token(time.Time{}, testParamPrefix+anything),
			wantStatus:    http.StatusForbidden,
			wantReason:    NoIssuedAt,
			wantWarnings:  []string{noIssuedAt},
		}, {
			name:          "a token without an issue time may carry unlimited capabilities",
			cfg:           enforcing,
			authorization: token(time.Time{}, testParamPrefix+deviceInfo),
			wantStatus:    http.StatusOK,
		}, {
			name: "an override does not exempt the token",
			cfg: paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce, MaxAge: limits,
				Overrides: map[string][]string{testPrincipal: {}}},
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusForbidden,
			wantReason:    TokenTooOld,
		}, {
			name:          "permissive with warnings: let through and told",
			cfg:           paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModePermissiveWithWarnings, MaxAge: limits},
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusOK,
			wantWarnings:  []string{wouldBeOld},
		}, {
			name:          "permissive: let through, counted, not told",
			cfg:           paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModePermissive, MaxAge: limits},
			silent:        true,
			authorization: token(stale, testParamPrefix+anything),
			wantStatus:    http.StatusOK,
			wantWarnings:  []string{wouldBeOld},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			v.Set(paramPrefixesKey, tc.cfg.Prefixes)
			cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: tc.cfg}}

			// the age limit is checked on every route, so no extractor is
			// involved: the plain chain is enough
			mw, checks, warnings := protectCounting(t, cfg, v, keys.provider)

			rec := serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", tc.authorization)
			})

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached(rec))

			got := rec.Header().Values(WarningHeader)
			if tc.silent {
				assert.Empty(t, got, "a silent mode writes no warning header")
			} else if tc.wantWarnings != nil {
				require.Len(t, got, len(tc.wantWarnings), "warnings: %v", got)
				for i, want := range tc.wantWarnings {
					assert.True(t, strings.HasPrefix(got[i], want), "warning %q should start with %q", got[i], want)
				}
			}

			if tc.wantStatus == http.StatusForbidden {
				rejected := checks.With(prometheus.Labels{
					OutcomeLabel:   Rejected,
					ReasonLabel:    tc.wantReason,
					ClientIDLabel:  testPrincipal,
					PartnerIDLabel: testPartner,
					EndpointLabel:  NoneEndpoint,
					MethodLabel:    http.MethodGet,
				})
				assert.Equal(t, 1.0, testutil.ToFloat64(rejected), "rejection counted")
				assert.Equal(t, 0, testutil.CollectAndCount(warnings), "the explanation of a refusal is not a warning")
			}

			if tc.cfg.Mode != ModeEnforce && tc.wantWarnings != nil {
				counted := warnings.With(prometheus.Labels{KindLabel: paramWarningKind, ReasonLabel: TokenTooOld, ClientIDLabel: testPrincipal})
				assert.Equal(t, 1.0, testutil.ToFloat64(counted), "would-reject counted")
			}
		})
	}
}

func TestParseParamAgeLimits(t *testing.T) {
	cases := []struct {
		name   string
		maxAge map[string]time.Duration
		valid  bool
	}{
		{name: "none", valid: true},
		{name: "pattern only", maxAge: map[string]time.Duration{anything: time.Minute}, valid: true},
		{name: "pattern with operation", maxAge: map[string]time.Duration{" " + anything + ":read ": time.Minute}, valid: true},
		{name: "unknown operation", maxAge: map[string]time.Duration{malformedAnything: time.Minute}},
		{name: "empty pattern", maxAge: map[string]time.Duration{":read": time.Minute}},
		{name: "zero age", maxAge: map[string]time.Duration{anything: 0}},
		{name: "negative age", maxAge: map[string]time.Duration{anything: -time.Minute}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			limits, err := parseParamAgeLimits(tc.maxAge)
			if !tc.valid {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Len(t, limits, len(tc.maxAge))
		})
	}
}

func TestParamMaxAgeFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    jwt:
      prefixes: ["webpa-prefix:api:"]
      param:
        prefixes: ["webpa-prefix:param:"]
        maxAge:
          "Device.**": 30m
          "Device.WiFi.Security.**:write": 15m
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg inboundConfig
	require.NoError(t, v.UnmarshalKey(inboundConfigKey, &cfg))

	// viper lowercases map keys; the limits match without regard to case
	assert.Equal(t, map[string]time.Duration{
		"device.**":                     30 * time.Minute,
		"device.wifi.security.**:write": 15 * time.Minute,
	}, cfg.JWT.Param.MaxAge)
}
