// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/tr1d1um/paramfilter"
)

const paramConfigKey = "auth.inbound.JWT.Param"

// paramWarningKind identifies the warnings the parameter check raises.
const paramWarningKind = "param"

// DefaultParamCacheSize is the number of parsed parameter capabilities a
// ParamCheck retains by default.
const DefaultParamCacheSize = 1024

// Operation is what a request does to the parameters it names.
type Operation string

const (
	// OperationRead covers getting values and attributes.
	OperationRead Operation = "read"

	// OperationWrite covers setting values and attributes and adding,
	// replacing or deleting table rows.
	OperationWrite Operation = "write"

	// operationAll is the capability suffix, and the meaning of no suffix,
	// that allows both operations.
	operationAll = "all"
)

// ParamRequest is what a device request does, and to which TR-181
// parameters.  A name ending in "." is a partial path: everything beneath
// it.
type ParamRequest struct {
	Operation Operation
	Names     []string
}

// ParamExtractor finds the parameters a request names.  ok is false when the
// request names none that can be checked, because the route carries no
// parameters or the request is malformed; the request then goes on to the
// handler, which rejects a malformed one itself.
type ParamExtractor func(*http.Request) (req ParamRequest, ok bool)

// paramConfig is the auth.inbound.JWT.Param section: how a token's parameter
// capabilities narrow which TR-181 parameters its bearer may touch.  The
// check runs only when the section is present.
type paramConfig struct {
	// Prefixes select a token's parameter capabilities, e.g.
	// webpa-prefix-param:.  A prefix is matched literally.  At least one is
	// required.
	Prefixes []string

	// Mode is ModePermissive (the default), ModePermissiveWithWarnings or
	// ModeEnforce.  A token with no parameter capability is unrestricted:
	// it may touch any parameter its endpoint capabilities allow.
	Mode string

	// Overrides are parameter capabilities, without the prefix, set here
	// for particular callers by principal, matched without regard to case
	// since the configuration loses it.  An override replaces whatever the
	// caller's tokens say, looser or stricter.  An empty list makes the
	// caller unrestricted.
	Overrides map[string][]string

	// CacheSize bounds how many parsed capabilities are retained.
	CacheSize int

	// MaxAge limits how long a token carrying a given parameter capability
	// is honored, by the capability's pattern with an optional operation
	// suffix, e.g. "Device.**": 30m.  A token older than the limit for any
	// capability it carries is refused on every request, whatever it asks
	// for, so a risky capability cannot outlive the deployment's tolerance
	// for it.  Overrides do not exempt a token.
	MaxAge map[string]time.Duration
}

// paramCapability is one parsed parameter capability.
type paramCapability struct {
	// text is the capability as it was given, without its prefix, for the
	// request logger.
	text string

	// pattern matches lowercased parameter names.
	pattern *regexp.Regexp

	// subtree is whether the pattern ends in **, so that it covers every
	// parameter beneath any partial path it matches.
	subtree bool

	read, write bool
}

// allows reports whether the capability lets op be done to name.
func (c paramCapability) allows(op Operation, name string) bool {
	switch op {
	case OperationRead:
		if !c.read {
			return false
		}
	case OperationWrite:
		if !c.write {
			return false
		}
	default:
		return false
	}

	if isPartialPath(name) && !c.subtree {
		return false
	}

	return c.pattern.MatchString(strings.ToLower(name))
}

// isPartialPath reports whether a name refers to everything beneath it.
func isPartialPath(name string) bool {
	return strings.HasSuffix(name, ".")
}

// parseParamCapability parses the remainder of a parameter capability after
// its prefix: <pattern>[:read|:write|:all].
func parseParamCapability(text string) (paramCapability, error) {
	pattern, op, found := strings.CutLast(text, ":")
	if !found {
		op = operationAll
	}

	if pattern == "" {
		return paramCapability{}, fmt.Errorf("empty pattern in %q", text)
	}

	pcap := paramCapability{text: text, subtree: strings.HasSuffix(pattern, "**")}
	switch Operation(op) {
	case OperationRead:
		pcap.read = true
	case OperationWrite:
		pcap.write = true
	case operationAll:
		pcap.read, pcap.write = true, true
	default:
		return paramCapability{}, fmt.Errorf("unknown operation %q in %q", op, text)
	}

	re, err := paramfilter.CompileGlob(pattern)
	if err != nil {
		return paramCapability{}, fmt.Errorf("bad pattern %q: %w", pattern, err)
	}

	pcap.pattern = re

	return pcap, nil
}

// parsedParamCapability is a capability string matched against the prefixes.
type parsedParamCapability struct {
	selected  bool // a prefix matched
	malformed bool
	paramCapability
}

// ParamCheck holds a token's bearer to the TR-181 parameters its parameter
// capabilities name.  It is applied to the device routes by a middleware
// that knows how to find the parameters in each kind of request; the check
// runs after bascule's middleware has verified the token.
//
// A nil ParamCheck, or one built from no configuration, checks nothing.
type ParamCheck struct {
	limits
	prefixes  []string
	mode      checkMode
	overrides map[string][]paramCapability
	l         *zap.Logger
	counter   *prometheus.CounterVec
	warnings  *prometheus.CounterVec
	endpoints []*regexp.Regexp

	// cache holds capabilities already matched against the prefixes and
	// parsed.  Capabilities come from tokens, so it is bounded: when full,
	// it is emptied.
	cacheSize int
	cacheLock sync.Mutex
	cache     map[string]parsedParamCapability
}

func newParamCheck(cfg paramConfig, l *zap.Logger, counter, warnings *prometheus.CounterVec, endpoints []*regexp.Regexp) (*ParamCheck, error) {
	mode, err := parseMode(cfg.Mode, paramConfigKey, ModePermissive, ModePermissiveWithWarnings, ModeEnforce)
	if err != nil {
		return nil, err
	}

	if len(cfg.Prefixes) == 0 {
		return nil, fmt.Errorf("`%s.Prefixes`: at least one parameter capability prefix is required", paramConfigKey)
	}

	if slices.Contains(cfg.Prefixes, "") {
		return nil, fmt.Errorf("`%s.Prefixes`: a prefix cannot be empty", paramConfigKey)
	}

	overrides := make(map[string][]paramCapability, len(cfg.Overrides))
	for principal, texts := range cfg.Overrides {
		caps := make([]paramCapability, 0, len(texts))
		for i, text := range texts {
			pcap, err := parseParamCapability(strings.TrimSpace(text))
			if err != nil {
				return nil, fmt.Errorf("`%s.Overrides[%q][%d]`: %w", paramConfigKey, principal, i, err)
			}

			caps = append(caps, pcap)
		}

		overrides[strings.ToLower(principal)] = caps
	}

	cacheSize := cfg.CacheSize
	if cacheSize <= 0 {
		cacheSize = DefaultParamCacheSize
	}

	return &ParamCheck{
		prefixes:  cfg.Prefixes,
		mode:      mode,
		overrides: overrides,
		l:         l,
		counter:   counter,
		warnings:  warnings,
		endpoints: endpoints,
		cacheSize: cacheSize,
		cache:     make(map[string]parsedParamCapability),
	}, nil
}

// Middleware applies the check to a route, finding each request's parameters
// with extract.  A nil ParamCheck leaves the route as it is.
func (c *ParamCheck) Middleware(extract ParamExtractor) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		if c == nil {
			return next
		}

		if extract == nil {
			panic("auth: a ParamCheck needs an extractor")
		}

		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			req, ok := extract(request)
			if !ok {
				next.ServeHTTP(response, request)
				return
			}

			token, _ := bascule.Get(request.Context())
			d := c.check(token, req)
			c.record(response.Header(), request, token, req, d)

			// the capabilities the caller was held to, for the request logger
			request = request.WithContext(addLogFields(request.Context(),
				zap.Strings(ParamField, capabilityTexts(d.caps))))

			if d.allowed || c.mode.permissive {
				next.ServeHTTP(response, request)
				return
			}

			response.WriteHeader(http.StatusForbidden)
		})
	}
}

// paramDecision is the outcome of checking one request.
type paramDecision struct {
	allowed bool

	// name is the first parameter no capability allows, when not allowed.
	name string

	// malformed are the token's malformed parameter capabilities.
	malformed []string

	// caps are the capabilities the caller was held to: their override, or
	// the well-formed ones their token carries.  Empty for an unrestricted
	// caller.
	caps []paramCapability
}

// capabilityTexts returns capabilities as they were given, for the request
// logger.
func capabilityTexts(caps []paramCapability) []string {
	texts := make([]string, 0, len(caps))
	for _, c := range caps {
		texts = append(texts, c.text)
	}

	return texts
}

// check decides whether the token may do what the request asks.  A token
// with no parameter capability, and whose caller has no override, is
// unrestricted.  A malformed capability counts as present but allows
// nothing.
func (c *ParamCheck) check(token bascule.Token, req ParamRequest) paramDecision {
	var (
		d       paramDecision
		present bool
		caps    []paramCapability
	)

	if token != nil {
		if override, ok := c.overrides[strings.ToLower(token.Principal())]; ok {
			present, caps = len(override) > 0, override
		} else {
			capabilities, _ := bascule.GetCapabilities(token)
			for _, capability := range capabilities {
				parsed := c.parse(capability)
				if !parsed.selected {
					continue
				}

				present = true
				if parsed.malformed {
					d.malformed = append(d.malformed, capability)
					continue
				}

				caps = append(caps, parsed.paramCapability)
			}
		}
	}

	d.caps = caps

	if !present {
		d.allowed = true
		return d
	}

	for _, name := range req.Names {
		if !allowed(caps, req.Operation, name) {
			d.name = name
			return d
		}
	}

	d.allowed = true

	return d
}

// allowed reports whether any capability lets op be done to name.
func allowed(caps []paramCapability, op Operation, name string) bool {
	for _, pcap := range caps {
		if pcap.allows(op, name) {
			return true
		}
	}

	return false
}

// parse matches a capability against the prefixes and parses it, through
// the cache.
func (c *ParamCheck) parse(capability string) parsedParamCapability {
	c.cacheLock.Lock()
	defer c.cacheLock.Unlock()

	if parsed, ok := c.cache[capability]; ok {
		return parsed
	}

	var parsed parsedParamCapability
	for _, prefix := range c.prefixes {
		text, ok := strings.CutPrefix(capability, prefix)
		if !ok {
			continue
		}

		parsed.selected = true
		pcap, err := parseParamCapability(text)
		if err != nil {
			parsed.malformed = true
		} else {
			parsed.paramCapability = pcap
		}

		break
	}

	if len(c.cache) >= c.cacheSize {
		clear(c.cache)
	}

	c.cache[capability] = parsed

	return parsed
}

// record reports a decision: malformed capabilities are counted and logged,
// a rejection is counted, logged and explained to the caller, and in a
// permissive mode a rejection is recorded as a warning instead.  When the
// mode tells the caller, each warning is also a response header.
func (c *ParamCheck) record(header http.Header, request *http.Request, token bascule.Token, req ParamRequest, d paramDecision) {
	var client, partner string
	if token != nil {
		client = token.Principal()
		partner = determinePartnerID(token)
	}

	for _, capability := range d.malformed {
		w := bascule.Warning{
			Reason: "malformed",
			Attrs: []bascule.WarningAttr{
				{Key: KindLabel, Value: paramWarningKind},
				{Key: "cap", Value: capability},
			},
		}

		if c.mode.warnings {
			header.Add(WarningHeader, w.String())
		}

		c.warnings.With(c.cleanLabels(prometheus.Labels{
			KindLabel:     paramWarningKind,
			ReasonLabel:   w.Reason,
			ClientIDLabel: client,
		})).Add(1)
	}

	if d.allowed {
		return
	}

	fields := []zap.Field{
		zap.String("jwt_rejection_reason", ParameterNotAllowed),
		zap.Strings(ParamField, capabilityTexts(d.caps)),
		zap.String("operation", string(req.Operation)),
		zap.String("parameter", c.value(d.name)),
	}

	// The request logger already names the caller and where they called
	// from; outside the chain, the service logger names the caller.
	l := requestLogger(request, c.l.With(
		zap.String(PrincipalField, c.value(client)),
		zap.String(PartnerField, c.value(partner))))

	if c.mode.permissive {
		w := bascule.Warning{
			Reason: warningWouldReject,
			Attrs: []bascule.WarningAttr{
				{Key: KindLabel, Value: paramWarningKind},
				{Key: ReasonLabel, Value: strings.ReplaceAll(ParameterNotAllowed, "_", "-")},
				{Key: "op", Value: string(req.Operation)},
				{Key: "name", Value: d.name},
			},
		}

		if c.mode.warnings {
			header.Add(WarningHeader, w.String())
		}

		c.warnings.With(c.cleanLabels(prometheus.Labels{
			KindLabel:     paramWarningKind,
			ReasonLabel:   ParameterNotAllowed,
			ClientIDLabel: client,
		})).Add(1)

		l.Info("parameter check: would reject", append(fields, zap.String("warning", c.warning(w.String())))...)

		return
	}

	// The caller is told why: the name is one they sent, and the token
	// has been verified, so there is nothing to hide from them.
	if c.mode.warnings {
		header.Add(WarningHeader, bascule.Warning{
			Reason: warningRejected,
			Attrs: []bascule.WarningAttr{
				{Key: KindLabel, Value: paramWarningKind},
				{Key: ReasonLabel, Value: strings.ReplaceAll(ParameterNotAllowed, "_", "-")},
				{Key: "op", Value: string(req.Operation)},
				{Key: "name", Value: d.name},
			},
		}.String())
	}

	c.counter.With(c.cleanLabels(prometheus.Labels{
		ClientIDLabel:  client,
		PartnerIDLabel: partner,
		EndpointLabel:  determineEndpoint(c.endpoints, request),
		MethodLabel:    request.Method,
		OutcomeLabel:   Rejected,
		ReasonLabel:    ParameterNotAllowed,
	})).Add(1)

	l.Info("parameter check: request rejected", fields...)
}
