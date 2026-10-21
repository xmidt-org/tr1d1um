// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/tokenrate"
)

const rateConfigKey = "auth.inbound.JWT.Rate"

// The window bounds applied when the configuration gives none.
const (
	defaultRateMinWindow = time.Minute
	defaultRateMaxWindow = 24 * time.Hour
)

// rateWarningKind identifies the warnings the rate check raises.
const rateWarningKind = "rate"

// rateConfig is the auth.inbound.JWT.Rate section: how a token's rate
// capabilities limit how fast its bearer may call this instance.  The check
// runs only when the section is present.
type rateConfig struct {
	// Prefixes select a token's rate capabilities, e.g. webpa-prefix-rate:.
	// A prefix is matched literally.  At least one is required.
	Prefixes []string

	// Mode is ModePermissive (the default), ModePermissiveWithWarnings or
	// ModeEnforce.  There is no required mode: a deployment that wants
	// every caller limited sets Defaults, as slow as it likes.
	Mode string

	// Defaults are the rates a caller is held to when their token carries
	// no rate capability and they have no override.  Each caller gets their
	// own allowance at these rates.  With none, such a token is
	// unrestricted.
	Defaults []string

	// MinWindow and MaxWindow bound the window a token's rate is held at.
	// A rate with a window outside them is rescaled to the nearer bound at
	// the same calls per second, rounded to the nearest call but never below
	// one.  They keep a tiny window from churning state and a huge one from
	// granting a huge burst.  Default to one minute and 24 hours.
	MinWindow time.Duration
	MaxWindow time.Duration

	// Overrides are rates set here for particular callers, by principal,
	// matched without regard to case since the configuration loses it.  An
	// override replaces whatever the caller's tokens say, looser or
	// stricter.  An empty list makes the caller unrestricted.
	Overrides map[string][]string
}

// rateLimiter holds each caller to the rates their tokens carry.  It runs
// after bascule's middleware, which has verified the token and put it in the
// request context, so the principal it limits by is never the client's to
// choose.
//
// tokenrate's own middleware is not used because it has no permissive mode
// and no hook for counting rejections the way the other checks are counted.
type rateLimiter struct {
	limits
	limiter   *tokenrate.Limiter
	mode      checkMode
	l         *zap.Logger
	counter   *prometheus.CounterVec
	warnings  *prometheus.CounterVec
	endpoints []*regexp.Regexp
}

func newRateLimiter(cfg rateConfig, l *zap.Logger, counter, warnings *prometheus.CounterVec, endpoints []*regexp.Regexp) (*rateLimiter, error) {
	mode, err := parseMode(cfg.Mode, rateConfigKey, ModePermissive, ModePermissiveWithWarnings, ModeEnforce)
	if err != nil {
		return nil, err
	}

	resolver, err := newRateResolver(cfg)
	if err != nil {
		return nil, err
	}

	limiter, err := tokenrate.New(
		tokenrate.WithPrefixes(cfg.Prefixes...),
		tokenrate.WithResolver(resolver),
	)
	if err != nil {
		return nil, err
	}

	return &rateLimiter{
		limiter:   limiter,
		mode:      mode,
		l:         l,
		counter:   counter,
		warnings:  warnings,
		endpoints: endpoints,
	}, nil
}

// newRateResolver builds the policy tokenrate consults on every request: a
// configured override replaces the token's rates; otherwise malformed rates
// are dropped beside valid ones, or hold the token to zero when there is
// nothing else, valid rates are held within the window bounds, and a token
// left with no rate is held to the defaults.  With no defaults either, the
// token is unrestricted.
func newRateResolver(cfg rateConfig) (tokenrate.Resolver, error) {
	minWindow, maxWindow, err := rateWindowBounds(cfg)
	if err != nil {
		return nil, err
	}

	defaults, err := parseRates(cfg.Defaults, rateConfigKey+".Defaults")
	if err != nil {
		return nil, err
	}

	overrides := make(map[string][]tokenrate.Rate, len(cfg.Overrides))
	for principal, texts := range cfg.Overrides {
		rates, err := parseRates(texts, fmt.Sprintf("%s.Overrides[%q]", rateConfigKey, principal))
		if err != nil {
			return nil, err
		}

		overrides[strings.ToLower(principal)] = rates
	}

	return func(_ context.Context, principal string, provided []tokenrate.Rate) ([]tokenrate.Rate, error) {
		if rates, ok := overrides[strings.ToLower(principal)]; ok {
			return rates, nil
		}

		if rates := boundRates(provided, minWindow, maxWindow); len(rates) > 0 {
			return rates, nil
		}

		return defaults, nil
	}, nil
}

// parseRates parses configured rates.  Configuration is trusted, so they are
// used as written: the window bounds do not apply.  An error names the
// position of the bad entry under key.
func parseRates(texts []string, key string) ([]tokenrate.Rate, error) {
	rates := make([]tokenrate.Rate, 0, len(texts))
	for i, text := range texts {
		rate, err := tokenrate.ParseRate(strings.TrimSpace(text))
		if err != nil {
			return nil, fmt.Errorf("`%s[%d]`: %w", key, i, err)
		}

		rates = append(rates, rate)
	}

	return rates, nil
}

// rateWindowBounds returns the configured window bounds, or the defaults for
// any left unset.
func rateWindowBounds(cfg rateConfig) (minWindow, maxWindow time.Duration, err error) {
	minWindow, maxWindow = cfg.MinWindow, cfg.MaxWindow
	if minWindow == 0 {
		minWindow = defaultRateMinWindow
	}

	if maxWindow == 0 {
		maxWindow = defaultRateMaxWindow
	}

	switch {
	case minWindow < 0:
		return 0, 0, fmt.Errorf("`%s.MinWindow`: must be positive, got %s", rateConfigKey, minWindow)
	case maxWindow < minWindow:
		return 0, 0, fmt.Errorf("`%s.MaxWindow`: must not be shorter than MinWindow, got %s and %s", rateConfigKey, maxWindow, minWindow)
	}

	return minWindow, maxWindow, nil
}

// boundRates is what a token's rates count as.  Malformed capabilities reach
// the resolver as zero rates: beside valid rates they are dropped, and with
// nothing valid they are returned as they are, so the token is held to zero
// rather than becoming unrestricted.  A valid rate whose window is outside
// the bounds is rescaled to the nearer bound.
func boundRates(provided []tokenrate.Rate, minWindow, maxWindow time.Duration) []tokenrate.Rate {
	valid := slices.DeleteFunc(slices.Clone(provided), tokenrate.Rate.IsZero)
	if len(valid) == 0 {
		return provided
	}

	for i, r := range valid {
		switch {
		case r.Window < minWindow:
			valid[i] = rescaleRate(r, minWindow)
		case r.Window > maxWindow:
			valid[i] = rescaleRate(r, maxWindow)
		}
	}

	return valid
}

// rescaleRate returns r at window, keeping the same calls per second.  The
// count is rounded to the nearest call but never below one, and never above
// what an int holds.  The result is a new rate, so it reads back formatted
// rather than as the client wrote it.
func rescaleRate(r tokenrate.Rate, window time.Duration) tokenrate.Rate {
	// count * window / r.Window, rounded half up, without overflowing
	count := new(big.Int).Mul(big.NewInt(int64(r.Count)), big.NewInt(int64(window)))
	count.Add(count, big.NewInt(int64(r.Window/2)))
	count.Div(count, big.NewInt(int64(r.Window)))

	switch {
	case !count.IsInt64() || count.Int64() > math.MaxInt:
		return tokenrate.Rate{Count: math.MaxInt, Window: window}
	case count.Int64() < 1:
		return tokenrate.Rate{Count: 1, Window: window}
	default:
		return tokenrate.Rate{Count: int(count.Int64()), Window: window}
	}
}

// handle is the alice constructor that applies the limiter.
func (rl *rateLimiter) handle(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var (
			principal    string
			capabilities []string
		)

		// Without a token nothing can be limited; bascule's middleware
		// always runs first, so this only happens if the chain is misbuilt.
		token, ok := bascule.Get(request.Context())
		if ok {
			principal = token.Principal()
			capabilities, _ = bascule.GetCapabilities(token)
		}

		decision := rl.limiter.Check(request.Context(), principal, capabilities)
		rl.record(response.Header(), request, token, decision)

		// the rates the caller was held to, for the request logger
		request = request.WithContext(addLogFields(request.Context(),
			zap.Strings(RateField, rateTexts(decision.Limits))))

		if decision.Allowed || rl.mode.permissive {
			next.ServeHTTP(response, request)
			return
		}

		if decision.Reason == tokenrate.RateExceeded {
			if decision.RetryAfter > 0 {
				response.Header().Set("Retry-After", retryAfterSeconds(decision.RetryAfter))
			}

			response.WriteHeader(http.StatusTooManyRequests)
			return
		}

		response.WriteHeader(http.StatusForbidden)
	})
}

// record reports a decision: malformed capabilities are counted and logged
// on every request, a rejection is counted and logged, and in a permissive
// mode a rejection is recorded as a warning instead.  When the mode tells
// the caller, each warning is also a response header.  bascule's middleware
// has already written its warning headers by the time this runs, so this
// writes its own.
func (rl *rateLimiter) record(header http.Header, request *http.Request, token bascule.Token, d tokenrate.Decision) {
	var client, partner string
	if token != nil {
		client = token.Principal()
		partner = determinePartnerID(token)
	}

	for _, w := range d.Warnings {
		if rl.mode.warnings {
			header.Add(WarningHeader, w.String())
		}

		rl.warnings.With(rl.cleanLabels(prometheus.Labels{
			KindLabel:     rateWarningKind,
			ReasonLabel:   strings.ReplaceAll(w.Reason, "-", "_"),
			ClientIDLabel: client,
		})).Add(1)
	}

	if d.Allowed {
		return
	}

	reason := rateRejectionReason(d)
	fields := []zap.Field{
		zap.String("jwt_rejection_reason", reason),
		zap.Strings(RateField, rateTexts(d.Limits)),
		zap.Stringer("limit", d.Limit),
		zap.Duration("retry_after", d.RetryAfter),
	}

	// The request logger already names the caller and where they called
	// from; outside the chain, the service logger names the caller.
	l := requestLogger(request, rl.l.With(
		zap.String(PrincipalField, rl.value(client)),
		zap.String(PartnerField, rl.value(partner))))

	// The resolver never errs, so a denial means tokenrate rejected what it
	// returned.
	if d.Reason == tokenrate.Denied {
		l.Error("rate limiter failure", append(fields, zap.Error(d.Err))...)
	}

	if rl.mode.permissive {
		w := bascule.Warning{
			Reason: warningWouldReject,
			Attrs: []bascule.WarningAttr{
				{Key: KindLabel, Value: rateWarningKind},
				{Key: ReasonLabel, Value: strings.ReplaceAll(reason, "_", "-")},
			},
		}
		if limit := d.Limit.String(); limit != "" {
			w.Attrs = append(w.Attrs, bascule.WarningAttr{Key: "limit", Value: limit})
		}

		if rl.mode.warnings {
			header.Add(WarningHeader, w.String())
		}

		rl.warnings.With(rl.cleanLabels(prometheus.Labels{
			KindLabel:     rateWarningKind,
			ReasonLabel:   reason,
			ClientIDLabel: client,
		})).Add(1)

		l.Info("rate limiter: would reject", append(fields, zap.Stringer("warning", w))...)

		return
	}

	rl.counter.With(rl.cleanLabels(prometheus.Labels{
		ClientIDLabel:  client,
		PartnerIDLabel: partner,
		EndpointLabel:  determineEndpoint(rl.endpoints, request),
		MethodLabel:    request.Method,
		OutcomeLabel:   Rejected,
		ReasonLabel:    reason,
	})).Add(1)

	l.Info("rate limiter: request rejected", fields...)
}

// rateTexts returns rates as text, for the request logger.
func rateTexts(rates []tokenrate.Rate) []string {
	texts := make([]string, 0, len(rates))
	for _, r := range rates {
		texts = append(texts, r.String())
	}

	return texts
}

// rateRejectionReason names why a decision refused the request, as a metric
// label value.
func rateRejectionReason(d tokenrate.Decision) string {
	if d.Reason == tokenrate.RateExceeded {
		return RateExceeded
	}

	return RateDenied
}

// retryAfterSeconds formats a wait as the whole seconds Retry-After takes,
// rounded up so that a retry is never early.
func retryAfterSeconds(d time.Duration) string {
	return strconv.FormatInt(int64((d+time.Second-1)/time.Second), 10)
}
