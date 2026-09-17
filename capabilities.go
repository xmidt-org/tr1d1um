// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"go.uber.org/zap"
)

// A capability carried by a token has three parts:
//
//	example-prefix:api:device/.*/config:get
//	└──────────┬───────┘└──────┬─────┘ └┬┘
//	         prefix         endpoint  method
//
// The prefix is configured and identifies capabilities meant for this service.
// After it, endpoint is a regular expression matched against the request path
// and method is the HTTP verb (or the accept-all value).
//
// A token may carry capabilities with a different prefix -- future work will
// add tr181, cidr and rate limits under their own prefixes -- and those are
// ignored here, so an issuer can mint them before this build consumes them.
//
// Authorization is a grant: a request is permitted only when some capability
// matches it.  A request whose token carries no matching capability falls back
// to the configured default; with no default, nothing grants it.

// CapabilityMode is how the check participates in authorization.
type CapabilityMode string

const (
	// CapabilityDisabled turns the check off.  It is the explicit,
	// deliberate off; an omitted mode is off too, but is logged rather than
	// silent.
	CapabilityDisabled CapabilityMode = "disabled"

	// CapabilityMonitor evaluates the check and records the outcome, but lets
	// the request through.  It is the safe rung before enforce.
	CapabilityMonitor CapabilityMode = "monitor"

	// CapabilityEnforce rejects a request that no capability grants.
	CapabilityEnforce CapabilityMode = "enforce"
)

// parseMode maps the configured mode onto a value and reports whether checking
// is enabled.  An omitted or "disabled" mode is off; enforce and monitor are
// on; anything else -- a typo in a security setting -- is an error rather than a
// silent fallback.
func parseMode(s string) (mode CapabilityMode, enabled bool, err error) {
	switch mode = CapabilityMode(strings.TrimSpace(s)); mode {
	case "", CapabilityDisabled:
		return mode, false, nil
	case CapabilityMonitor, CapabilityEnforce:
		return mode, true, nil
	default:
		return mode, false, fmt.Errorf(
			"mode %q is not recognized; use %q, %q, or %q",
			s, CapabilityEnforce, CapabilityMonitor, CapabilityDisabled)
	}
}

// CapabilityConfig is the capabilityCheck configuration.  Its field names and
// meanings are unchanged from earlier releases, so an existing configuration
// works as written.
type CapabilityConfig struct {
	// Type is the mode: "enforce", "monitor", "disabled", or omitted (off).
	// Any other value stops the server rather than quietly disabling the check.
	Type string `mapstructure:"type"`

	// Prefix is the leading part of every capability this check consumes, up to
	// and including the segment before the endpoint, e.g. "example-prefix:api:".
	// Required when checking is enabled.
	Prefix string `mapstructure:"prefix"`

	// AcceptAllMethod is the method value in a capability that grants every
	// HTTP method.  Defaults to "all".
	AcceptAllMethod string `mapstructure:"acceptAllMethod"`

	// EndpointBuckets group requests by endpoint for the metric label.
	EndpointBuckets []string `mapstructure:"endpointBuckets"`

	// Default holds "{endpoint}:{method}" values applied when a request's token
	// carries no matching capability.  Empty means such a request is granted
	// nothing.
	Default []string `mapstructure:"default"`

	// CheckBasicAuth subjects basic-auth credentials to the check.  Defaults to
	// false: historically a valid basic credential was allowed everything and
	// the check applied only to bearer tokens.
	CheckBasicAuth bool `mapstructure:"checkBasicAuth"`
}

// Check evaluates a token's capabilities against a request.
type Check interface {
	// Allow returns nil when some capability grants the request, and an
	// error describing the denial otherwise.
	Allow(r *http.Request) error
}

// CapabilityApprover authorizes requests against a token's capabilities.
type CapabilityApprover struct {
	enabled        bool
	prefix         string
	mode           CapabilityMode
	checker        apiChecker
	fallback       Check
	checkBasicAuth bool
	logger         *zap.Logger
	metric         capabilityMetric
}

// NewCapabilityApprover builds an approver from configuration.  The api default
// is compiled here so a bad policy fails at startup, not on the first request.
func NewCapabilityApprover(cfg CapabilityConfig, logger *zap.Logger, metric capabilityMetric) (*CapabilityApprover, error) {
	mode, enabled, err := parseMode(cfg.Type)
	if err != nil {
		return nil, fmt.Errorf("capabilityCheck: type: %w", err)
	}

	if !enabled {
		// Authorization is off.  That is valid -- it may be handled elsewhere --
		// but it is logged rather than silent, since it is the difference
		// between "no policy" and "a policy that does nothing".
		logger.Warn("capability authorization is disabled")
		return &CapabilityApprover{logger: logger, metric: metric}, nil
	}

	if cfg.Prefix == "" {
		return nil, errors.New("capabilityCheck: prefix is required when checking is enabled")
	}

	acceptAll := cfg.AcceptAllMethod
	if acceptAll == "" {
		acceptAll = "all"
	}
	checker := apiChecker{acceptAllMethod: acceptAll}

	fallback, err := checker.Compile(cfg.Default)
	if err != nil {
		return nil, fmt.Errorf("capabilityCheck: default: %w", err)
	}

	return &CapabilityApprover{
		enabled:        true,
		prefix:         cfg.Prefix,
		mode:           mode,
		checker:        checker,
		fallback:       fallback,
		checkBasicAuth: cfg.CheckBasicAuth,
		logger:         logger,
		metric:         metric,
	}, nil
}

// Approve implements bascule.Approver[*http.Request].  When enabled, some
// capability must grant the request.
func (a *CapabilityApprover) Approve(_ context.Context, r *http.Request, token bascule.Token) error {
	if !a.enabled {
		return nil
	}

	// Basic credentials historically bypassed capability checking entirely.
	// Preserve that unless the deployment opts in.
	if _, isBasic := token.(basculehttp.BasicToken); isBasic && !a.checkBasicAuth {
		return nil
	}

	// The token's capabilities carrying the configured prefix, minus that
	// prefix, are "{endpoint}:{method}" specs for this check.
	specs := a.matchingCapabilities(token)

	check := a.fallback
	fromToken := false
	if len(specs) > 0 {
		compiled, err := a.checker.Compile(specs)
		if err != nil {
			// The token's own capabilities are malformed.  That grants
			// nothing; it is not a server error.
			a.logger.Warn("capability compile failed", zap.Error(err))
			compiled = nil
		}
		check = compiled
		fromToken = true
	}

	denial := errNoCapability
	if check != nil {
		denial = check.Allow(r)
	}

	if denial == nil {
		a.metric.record(capabilityLabelAPI, outcomeAllowed, r)
		return nil
	}

	outcome := outcomeRejected
	if a.mode == CapabilityMonitor {
		outcome = outcomeMonitored
	}
	a.metric.record(capabilityLabelAPI, outcome, r)

	a.logger.Warn("capability check denied request",
		zap.String("mode", string(a.mode)),
		zap.String("method", r.Method),
		zap.String("path", r.URL.EscapedPath()),
		zap.String("principal", token.Principal()),
		zap.Bool("from_token", fromToken),
		zap.Error(denial),
	)

	if a.mode == CapabilityMonitor {
		return nil
	}
	return fmt.Errorf("capability check failed: %w", denial)
}

// matchingCapabilities returns the "{endpoint}:{method}" spec of each of the
// token's capabilities that carry the configured prefix, with the prefix
// removed.  Capabilities with a different prefix -- including the tr181, cidr
// and rate capabilities to come -- are ignored, so an issuer may mint one
// before this build consumes it.
func (a *CapabilityApprover) matchingCapabilities(token bascule.Token) []string {
	accessor, ok := token.(bascule.CapabilitiesAccessor)
	if !ok {
		return nil
	}

	var specs []string
	for _, capability := range accessor.Capabilities() {
		if spec, found := strings.CutPrefix(capability, a.prefix); found {
			specs = append(specs, spec)
		}
	}
	return specs
}
