// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"math"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/tokenrate"
)

const (
	testRatePrefix  = "x1:webpa:rate:"
	ratePrefixesKey = rateConfigKey + ".Prefixes"
	rateModeKey     = rateConfigKey + ".Mode"

	// onePerMinute is the tightest rate the window bounds allow, so one
	// request is allowed and the next refused.
	onePerMinute = "1/1m"

	// malformedRate is how every malformed rate warning starts.
	malformedRate = "malformed; kind=rate"
)

// rateStep is one request of a rate limiting scenario.
type rateStep struct {
	caps           []string // rate capabilities, beside the granting endpoint one
	wantStatus     int
	wantRetryAfter bool
	wantWarnings   []string // prefixes, one per expected warning header
}

func TestNewMiddleware_Rate(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	// token signs a token for testPrincipal granting the endpoint, with the
	// given rate capabilities.  Each call produces a distinct token.
	token := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:get"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	ok := func(caps ...string) rateStep { return rateStep{caps: caps, wantStatus: http.StatusOK} }
	tooMany := func(caps ...string) rateStep {
		return rateStep{caps: caps, wantStatus: http.StatusTooManyRequests, wantRetryAfter: true}
	}

	cases := []struct {
		name           string
		cfg            rateConfig
		rateConfigured bool // whether the rate section is present at all
		defaultMode    bool // leave cfg.Mode unset; otherwise an unset mode is ModeEnforce here
		silent         bool // the mode keeps wantWarnings from the caller; they are only counted
		steps          []rateStep
		rejectReason   string // the rejection reason label for every non-200 step
	}{
		{
			name:  "rate not configured: rate capabilities are ignored",
			steps: []rateStep{ok(testRatePrefix + onePerMinute), ok(testRatePrefix + onePerMinute)},
		}, {
			name:           "no rate capability is unrestricted",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps:          []rateStep{ok(), ok(), ok()},
		}, {
			name:           "another rate prefix is ignored",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps:          []rateStep{ok("other:rate:1/1m"), ok("other:rate:1/1m")},
		}, {
			name:           "the count may be used at once, then the caller waits",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{
				ok(testRatePrefix + "2/1m"),
				ok(testRatePrefix + "2/1m"),
				tooMany(testRatePrefix + "2/1m"),
				tooMany(testRatePrefix + "2/1m"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "every rate on a token applies",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{
				ok(testRatePrefix+"100/1m", testRatePrefix+"1/24h"),
				tooMany(testRatePrefix+"100/1m", testRatePrefix+"1/24h"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "a cut lands on every token the caller holds",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{
				ok(testRatePrefix + "5/1m"),
				ok(testRatePrefix + onePerMinute),
				tooMany(testRatePrefix + "5/1m"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "a short window is held at one minute, so the burst is rescaled",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{
				// 1/20s is held as 3/1m
				ok(testRatePrefix + "1/20s"),
				ok(testRatePrefix + "1/20s"),
				ok(testRatePrefix + "1/20s"),
				tooMany(testRatePrefix + "1/20s"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "a configured minimum window is honored",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, MinWindow: 10 * time.Second},
			steps: []rateStep{
				// 1/20s is within the bounds now, so only one call bursts
				ok(testRatePrefix + "1/20s"),
				tooMany(testRatePrefix + "1/20s"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "a configured maximum window is honored",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, MaxWindow: time.Hour},
			steps: []rateStep{
				// 2/2h is held as 1/1h
				ok(testRatePrefix + "2/2h"),
				tooMany(testRatePrefix + "2/2h"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "a rescaled rate is reported at its held window",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissiveWithWarnings},
			steps: []rateStep{
				ok(testRatePrefix + "1/30s"),
				ok(testRatePrefix + "1/30s"),
				{
					caps:         []string{testRatePrefix + "1/30s"},
					wantStatus:   http.StatusOK,
					wantWarnings: []string{`would-reject; kind=rate; reason=rate-exceeded; limit="2/1m"`},
				},
			},
			rejectReason: RateExceeded,
		}, {
			name:           "only a malformed rate is held to zero, not unrestricted",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{{
				caps:         []string{testRatePrefix + "10/0s"},
				wantStatus:   http.StatusTooManyRequests,
				wantWarnings: []string{`malformed; kind=rate; cap="x1:webpa:rate:10/0s"`},
			}},
			rejectReason: RateExceeded,
		}, {
			name:           "days are not a unit",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{{
				caps:         []string{testRatePrefix + "100/1d"},
				wantStatus:   http.StatusTooManyRequests,
				wantWarnings: []string{`malformed; kind=rate; cap="x1:webpa:rate:100/1d"`},
			}},
			rejectReason: RateExceeded,
		}, {
			name:           "a malformed rate beside a valid one warns on success",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			steps: []rateStep{{
				caps:         []string{testRatePrefix + "10/0s", testRatePrefix + "5/1m"},
				wantStatus:   http.StatusOK,
				wantWarnings: []string{malformedRate},
			}},
		}, {
			name:           "permissive with warnings: over the rate goes through with a warning",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissiveWithWarnings},
			steps: []rateStep{
				ok(testRatePrefix + onePerMinute),
				{
					caps:         []string{testRatePrefix + onePerMinute},
					wantStatus:   http.StatusOK,
					wantWarnings: []string{`would-reject; kind=rate; reason=rate-exceeded; limit="1/1m"`},
				},
			},
			rejectReason: RateExceeded,
		}, {
			name:           "defaults hold a token with no rate",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{onePerMinute}},
			steps:          []rateStep{ok(), tooMany()},
			rejectReason:   RateExceeded,
		}, {
			name:           "a token's own rate takes precedence over the defaults",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{onePerMinute}},
			steps:          []rateStep{ok(testRatePrefix + "5/1m"), ok(testRatePrefix + "5/1m")},
		}, {
			name:           "an override takes precedence over the defaults",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Defaults:  []string{onePerMinute},
				Overrides: map[string][]string{testPrincipal: {"5/1m"}},
			},
			steps: []rateStep{ok(), ok()},
		}, {
			name:           "only a malformed rate is held to zero, not to the defaults",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{"100/1m"}},
			steps: []rateStep{{
				caps:         []string{testRatePrefix + "10/0s"},
				wantStatus:   http.StatusTooManyRequests,
				wantWarnings: []string{malformedRate},
			}},
			rejectReason: RateExceeded,
		}, {
			name:           "defaults are used as written, outside the window bounds",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Defaults: []string{"1/1s"}},
			steps:          []rateStep{ok(), tooMany()},
			rejectReason:   RateExceeded,
		}, {
			name:           "permissive with warnings: over the defaults goes through with a warning",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissiveWithWarnings, Defaults: []string{onePerMinute}},
			steps: []rateStep{
				ok(),
				{
					wantStatus:   http.StatusOK,
					wantWarnings: []string{`would-reject; kind=rate; reason=rate-exceeded; limit="1/1m"`},
				},
			},
			rejectReason: RateExceeded,
		}, {
			name:           "permissive: over the rate is counted but the caller is not told",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissive},
			silent:         true,
			steps: []rateStep{
				ok(testRatePrefix + onePerMinute),
				{
					caps:         []string{testRatePrefix + onePerMinute},
					wantStatus:   http.StatusOK,
					wantWarnings: []string{"would-reject; kind=rate; reason=rate-exceeded"},
				},
			},
			rejectReason: RateExceeded,
		}, {
			name:           "permissive: a malformed rate is counted but the caller is not told",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModePermissive},
			silent:         true,
			steps: []rateStep{{
				caps:         []string{testRatePrefix + "10/0s", testRatePrefix + "5/1m"},
				wantStatus:   http.StatusOK,
				wantWarnings: []string{malformedRate},
			}},
		}, {
			name:           "the default mode is permissive: over the rate is only counted",
			rateConfigured: true,
			cfg:            rateConfig{Prefixes: []string{testRatePrefix}},
			defaultMode:    true,
			silent:         true,
			steps: []rateStep{
				ok(testRatePrefix + onePerMinute),
				{
					caps:         []string{testRatePrefix + onePerMinute},
					wantStatus:   http.StatusOK,
					wantWarnings: []string{"would-reject; kind=rate; reason=rate-exceeded"},
				},
			},
			rejectReason: RateExceeded,
		}, {
			name:           "an override replaces the token's rate",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Overrides: map[string][]string{testPrincipal: {onePerMinute}},
			},
			steps: []rateStep{
				ok(testRatePrefix + "100/1m"),
				tooMany(testRatePrefix + "100/1m"),
			},
			rejectReason: RateExceeded,
		}, {
			name:           "an override applies to a token with no rate",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Overrides: map[string][]string{testPrincipal: {onePerMinute}},
			},
			steps:        []rateStep{ok(), tooMany()},
			rejectReason: RateExceeded,
		}, {
			name:           "an empty override makes the caller unrestricted despite defaults",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Defaults:  []string{onePerMinute},
				Overrides: map[string][]string{testPrincipal: {}},
			},
			steps: []rateStep{ok(), ok(), ok(testRatePrefix + onePerMinute), ok(testRatePrefix + onePerMinute)},
		}, {
			name:           "an override matches the principal without regard to case",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Overrides: map[string][]string{strings.ToUpper(testPrincipal): {onePerMinute}},
			},
			steps:        []rateStep{ok(), tooMany()},
			rejectReason: RateExceeded,
		}, {
			name:           "an override for another caller does not apply",
			rateConfigured: true,
			cfg: rateConfig{
				Prefixes:  []string{testRatePrefix},
				Overrides: map[string][]string{"someone-else": {onePerMinute}},
			},
			steps: []rateStep{ok(testRatePrefix + "5/1m"), ok(testRatePrefix + "5/1m")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			if tc.rateConfigured {
				v.Set(ratePrefixesKey, tc.cfg.Prefixes)
			}

			// most scenarios are about what is rejected, so they enforce
			if tc.cfg.Mode == "" && !tc.defaultMode {
				tc.cfg.Mode = ModeEnforce
			}

			cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Rate: tc.cfg}}
			mw, checks, warnings := protectCounting(t, cfg, v, keys.provider)

			var rejected, wouldReject int
			for i, step := range tc.steps {
				rec := serveRequest(t, mw, func(req *http.Request) {
					req.Header.Set("Authorization", token(step.caps...))
				})

				assert.Equal(t, step.wantStatus, rec.Code, "step %d", i)
				assert.Equal(t, step.wantStatus == http.StatusOK, reached(rec), "step %d", i)

				retryAfter := rec.Header().Get("Retry-After")
				if step.wantRetryAfter {
					assert.NotEmpty(t, retryAfter, "step %d", i)
				} else {
					assert.Empty(t, retryAfter, "step %d", i)
				}

				got := rec.Header().Values(WarningHeader)
				if tc.silent {
					assert.Empty(t, got, "step %d: a silent mode writes no warning header", i)
				} else {
					require.Len(t, got, len(step.wantWarnings), "step %d warnings: %v", i, got)
					for j, want := range step.wantWarnings {
						assert.True(t, strings.HasPrefix(got[j], want), "step %d: warning %q should start with %q", i, got[j], want)
					}
				}

				for _, want := range step.wantWarnings {
					if strings.HasPrefix(want, "would-reject") {
						wouldReject++
					}
				}

				if step.wantStatus != http.StatusOK {
					rejected++
				}
			}

			if tc.rejectReason != "" {
				labels := prometheus.Labels{
					OutcomeLabel:   Rejected,
					ReasonLabel:    tc.rejectReason,
					ClientIDLabel:  testPrincipal,
					PartnerIDLabel: testPartner,
					EndpointLabel:  NoneEndpoint,
					MethodLabel:    http.MethodGet,
				}
				assert.Equal(t, float64(rejected), testutil.ToFloat64(checks.With(labels)), "rejections counted")

				permissive := warnings.With(prometheus.Labels{KindLabel: rateWarningKind, ReasonLabel: tc.rejectReason, ClientIDLabel: testPrincipal})
				assert.Equal(t, float64(wouldReject), testutil.ToFloat64(permissive), "would-rejects counted")
			}
		})
	}
}

func TestNewMiddleware_Rate_IsPerCaller(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	v := viper.New()
	v.Set(jwtPrefixesKey, []string{testPrefix})
	v.Set(ratePrefixesKey, []string{testRatePrefix})
	cfg := inboundConfig{JWT: jwtConfig{
		Prefixes: []string{testPrefix},
		Rate:     rateConfig{Prefixes: []string{testRatePrefix}, Mode: ModeEnforce},
	}}
	mw := protect(t, cfg, v, keys.provider)

	claims := map[string]any{
		capabilitiesClaim: []string{testPrefix + "device/.*/config:get", testRatePrefix + onePerMinute},
		allowedResources:  partners(testPartner),
	}
	first := keys.signAs(t, "caller-1", time.Now(), future, claims)
	second := keys.signAs(t, "caller-2", time.Now(), future, claims)

	for i, tc := range []struct {
		token      string
		wantStatus int
	}{
		{first, http.StatusOK},
		{first, http.StatusTooManyRequests},
		{second, http.StatusOK}, // another caller has its own allowance
		{second, http.StatusTooManyRequests},
	} {
		rec := serveRequest(t, mw, func(req *http.Request) {
			req.Header.Set("Authorization", "Bearer "+tc.token)
		})
		assert.Equal(t, tc.wantStatus, rec.Code, "request %d", i)
	}
}

func TestBoundRates(t *testing.T) {
	rate := func(count int, window time.Duration) tokenrate.Rate {
		return tokenrate.Rate{Count: count, Window: window}
	}

	parsed := func(text string) tokenrate.Rate {
		r, _ := tokenrate.ParseRate(text) // malformed text yields the zero rate, as tokenrate passes it
		return r
	}

	const (
		minute = time.Minute
		day    = 24 * time.Hour
	)

	cases := []struct {
		name       string
		provided   []tokenrate.Rate
		minWindow  time.Duration
		maxWindow  time.Duration
		want       []tokenrate.Rate
		wantString []string
	}{
		{
			name: "nothing stays nothing",
		}, {
			name:       "within the bounds is unchanged, keeping its text",
			provided:   []tokenrate.Rate{parsed("5/1m"), parsed("5/24h"), parsed("4/90m")},
			want:       []tokenrate.Rate{parsed("5/1m"), parsed("5/24h"), parsed("4/90m")},
			wantString: []string{"5/1m", "5/24h", "4/90m"},
		}, {
			name:       "a short window is held at the minimum",
			provided:   []tokenrate.Rate{parsed("100/1s")},
			want:       []tokenrate.Rate{rate(6000, minute)},
			wantString: []string{"6000/1m"},
		}, {
			name:       "a long window is held at the maximum",
			provided:   []tokenrate.Rate{parsed("100/48h")},
			want:       []tokenrate.Rate{rate(50, day)},
			wantString: []string{"50/24h"},
		}, {
			name:       "rounding is to the nearest call",
			provided:   []tokenrate.Rate{parsed("100/25h")},
			want:       []tokenrate.Rate{rate(96, day)},
			wantString: []string{"96/24h"},
		}, {
			name:     "never below one call",
			provided: []tokenrate.Rate{parsed("1/720h")},
			want:     []tokenrate.Rate{rate(1, day)},
		}, {
			name:     "never above what an int holds",
			provided: []tokenrate.Rate{rate(math.MaxInt, time.Nanosecond)},
			want:     []tokenrate.Rate{rate(math.MaxInt, minute)},
		}, {
			name:     "a malformed rate beside a valid one is dropped",
			provided: []tokenrate.Rate{parsed("10/0s"), parsed("5/1m")},
			want:     []tokenrate.Rate{parsed("5/1m")},
		}, {
			name:       "only malformed rates are returned as they are",
			provided:   []tokenrate.Rate{parsed("10/0s"), parsed("abc/1m")},
			want:       []tokenrate.Rate{parsed("10/0s"), parsed("abc/1m")},
			wantString: []string{"10/0s", "abc/1m"},
		}, {
			name:      "other bounds",
			provided:  []tokenrate.Rate{parsed("100/1ms"), parsed("100/2h"), parsed("7/30m")},
			minWindow: time.Second,
			maxWindow: time.Hour,
			want:      []tokenrate.Rate{rate(100000, time.Second), rate(50, time.Hour), parsed("7/30m")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			minWindow, maxWindow := tc.minWindow, tc.maxWindow
			if minWindow == 0 {
				minWindow, maxWindow = minute, day
			}

			before := slices.Clone(tc.provided)
			got := boundRates(tc.provided, minWindow, maxWindow)

			assert.Equal(t, tc.want, got)
			assert.Equal(t, before, tc.provided, "the input is not modified")

			for i, want := range tc.wantString {
				assert.Equal(t, want, got[i].String())
			}
		})
	}
}

func TestRateWindowBounds(t *testing.T) {
	cases := []struct {
		name    string
		cfg     rateConfig
		wantMin time.Duration
		wantMax time.Duration
		valid   bool
	}{
		{name: "defaults", wantMin: time.Minute, wantMax: 24 * time.Hour, valid: true},
		{name: "only min", cfg: rateConfig{MinWindow: time.Second}, wantMin: time.Second, wantMax: 24 * time.Hour, valid: true},
		{name: "only max", cfg: rateConfig{MaxWindow: time.Hour}, wantMin: time.Minute, wantMax: time.Hour, valid: true},
		{name: "equal", cfg: rateConfig{MinWindow: time.Hour, MaxWindow: time.Hour}, wantMin: time.Hour, wantMax: time.Hour, valid: true},
		{name: "max below default min", cfg: rateConfig{MaxWindow: time.Second}},
		{name: "max below min", cfg: rateConfig{MinWindow: time.Hour, MaxWindow: time.Minute}},
		{name: "negative min", cfg: rateConfig{MinWindow: -time.Second}},
		{name: "negative max", cfg: rateConfig{MaxWindow: -time.Second}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotMin, gotMax, err := rateWindowBounds(tc.cfg)
			if !tc.valid {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantMin, gotMin)
			assert.Equal(t, tc.wantMax, gotMax)
		})
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for d, want := range map[time.Duration]string{
		time.Millisecond:                 "1",
		time.Second:                      "1",
		time.Second + time.Millisecond:   "2",
		12*time.Second - time.Nanosecond: "12",
	} {
		assert.Equal(t, want, retryAfterSeconds(d), d.String())
	}
}

// The rate keys are documented in tr1d1um.yaml; this checks that they land on
// the right fields.
func TestRateConfigFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    jwt:
      prefixes: ["x1:webpa:api:"]
      rate:
        prefixes: ["x1:webpa:rate:"]
        mode: permissiveWithWarnings
        minWindow: 30s
        maxWindow: 12h
        defaults: ["600/1m", "50000/24h"]
        overrides:
          partner-x: ["50/1m", "10000/24h"]
          exempt: []
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg inboundConfig
	require.NoError(t, v.UnmarshalKey(inboundConfigKey, &cfg))

	assert.True(t, v.IsSet(rateConfigKey))
	assert.Equal(t, []string{"x1:webpa:rate:"}, cfg.JWT.Rate.Prefixes)
	assert.Equal(t, ModePermissiveWithWarnings, cfg.JWT.Rate.Mode)
	assert.Equal(t, 30*time.Second, cfg.JWT.Rate.MinWindow)
	assert.Equal(t, 12*time.Hour, cfg.JWT.Rate.MaxWindow)
	assert.Equal(t, []string{"600/1m", "50000/24h"}, cfg.JWT.Rate.Defaults)
	assert.Equal(t, []string{"50/1m", "10000/24h"}, cfg.JWT.Rate.Overrides["partner-x"])
	exempt, ok := cfg.JWT.Rate.Overrides["exempt"]
	assert.True(t, ok)
	assert.Empty(t, exempt)
}
