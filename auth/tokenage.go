// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculejwt"
)

// The errors paramAgeApprover.Approve returns, each joined with
// bascule.ErrUnauthorized.
var (
	// errTokenTooOld indicates the token carries a parameter capability
	// with a maximum age, and is older than it.
	errTokenTooOld = errors.New("token too old for a capability it carries")

	// errNoIssuedAt indicates the token carries a parameter capability with
	// a maximum age, but no iat claim to measure its age from.
	errNoIssuedAt = errors.New("token has no issued-at time for a capability with a maximum age")
)

// paramAgeLimit is one configured maximum age: a parameter capability
// pattern and, optionally, an operation.
type paramAgeLimit struct {
	// pattern is lowercased, so that it matches without regard to case.
	pattern string

	// op is the operation the limit applies to, or "" for any.
	op string

	maxAge time.Duration

	// text is the limit as configured, for messages.
	text string
}

// matches reports whether a capability's pattern and operation fall under
// this limit.  A capability with no suffix has the operation all; one with a
// suffix nobody recognizes still has a pattern, and a limit with no
// operation still applies to it.
func (l paramAgeLimit) matches(pattern, op string) bool {
	return strings.ToLower(pattern) == l.pattern && (l.op == "" || l.op == op)
}

// parseParamAgeLimits parses the configured maximum ages, keyed by pattern
// with an optional operation suffix.
func parseParamAgeLimits(maxAge map[string]time.Duration) ([]paramAgeLimit, error) {
	limits := make([]paramAgeLimit, 0, len(maxAge))
	for text, age := range maxAge {
		pattern, op := splitParamCapability(strings.TrimSpace(text))
		if pattern == "" {
			return nil, fmt.Errorf("`%s.MaxAge[%q]`: empty pattern", paramConfigKey, text)
		}

		if op != "" {
			switch Operation(op) {
			case OperationRead, OperationWrite, operationAll:
			default:
				return nil, fmt.Errorf("`%s.MaxAge[%q]`: unknown operation %q", paramConfigKey, text, op)
			}
		}

		if age <= 0 {
			return nil, fmt.Errorf("`%s.MaxAge[%q]`: the age must be positive, got %s", paramConfigKey, text, age)
		}

		limits = append(limits, paramAgeLimit{
			pattern: strings.ToLower(pattern),
			op:      op,
			maxAge:  age,
			text:    text,
		})
	}

	return limits, nil
}

// splitParamCapability splits the remainder of a parameter capability after
// its prefix into its pattern and operation suffix.  The operation is "" when
// there is no suffix.
func splitParamCapability(text string) (pattern, op string) {
	if i := strings.LastIndexByte(text, ':'); i >= 0 {
		return text[:i], text[i+1:]
	}

	return text, ""
}

// paramAgeApprover refuses a token that carries a parameter capability with
// a configured maximum age, once the token is older than that age, whatever
// the request asks for.  A risky capability then cannot outlive the
// deployment's tolerance for it, however long the issuer let the token
// live; a caller that does not need such a capability should not claim it.
//
// The approver runs in bascule's authorizer, so it covers every route.
type paramAgeApprover struct {
	prefixes []string
	limits   []paramAgeLimit
	mode     checkMode
	now      func() time.Time
}

func newParamAgeApprover(cfg paramConfig, mode checkMode) (*paramAgeApprover, error) {
	limits, err := parseParamAgeLimits(cfg.MaxAge)
	if err != nil {
		return nil, err
	}

	return &paramAgeApprover{
		prefixes: cfg.Prefixes,
		limits:   limits,
		mode:     mode,
		now:      time.Now,
	}, nil
}

// Approve implements bascule.Approver.  The first capability found past its
// limit decides.
func (a *paramAgeApprover) Approve(ctx context.Context, _ *http.Request, token bascule.Token) error {
	capabilities, _ := bascule.GetCapabilities(token)

	var issuedAt time.Time
	if claims, ok := token.(basculejwt.Claims); ok {
		issuedAt = claims.IssuedAt()
	}

	for _, capability := range capabilities {
		limit, ok := a.limitFor(capability)
		if !ok {
			continue
		}

		if issuedAt.IsZero() {
			return a.reject(ctx, errNoIssuedAt, "no-issued-at", capability, limit, 0)
		}

		if age := a.now().Sub(issuedAt); age > limit.maxAge {
			return a.reject(ctx, errTokenTooOld, "token-too-old", capability, limit, age)
		}
	}

	return nil
}

// limitFor finds the limit a capability falls under, if any.
func (a *paramAgeApprover) limitFor(capability string) (paramAgeLimit, bool) {
	for _, prefix := range a.prefixes {
		text, ok := strings.CutPrefix(capability, prefix)
		if !ok {
			continue
		}

		pattern, op := splitParamCapability(text)
		if op == "" {
			op = operationAll
		}

		for _, limit := range a.limits {
			if limit.matches(pattern, op) {
				return limit, true
			}
		}

		break
	}

	return paramAgeLimit{}, false
}

// reject returns err, or in a permissive mode raises a warning instead.  In
// every mode the caller is told why, as a warning the middleware writes when
// its mode allows.
func (a *paramAgeApprover) reject(ctx context.Context, err error, reason, capability string, limit paramAgeLimit, age time.Duration) error {
	w := bascule.Warning{
		Reason: warningRejected,
		Attrs: []bascule.WarningAttr{
			{Key: KindLabel, Value: paramWarningKind},
			{Key: ReasonLabel, Value: reason},
			{Key: "cap", Value: capability},
			{Key: "maxAge", Value: limit.maxAge.String()},
		},
	}

	if age > 0 {
		w.Attrs = append(w.Attrs, bascule.WarningAttr{Key: "age", Value: age.Truncate(time.Second).String()})
	}

	if a.mode.permissive {
		w.Reason = warningWouldReject
		bascule.AddWarning(ctx, w)

		return nil
	}

	bascule.AddWarning(ctx, w)

	return errors.Join(bascule.ErrUnauthorized, fmt.Errorf("%w: %s is limited to %s by %q", err, capability, limit.maxAge, limit.text))
}
