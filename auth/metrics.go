// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"math"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cast"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/clortho"
)

const Wildcard = "*"

// Names for our metrics
const (
	AuthCapabilityCheckCount   = "auth_capability_check"
	AuthCapabilityWarningCount = "auth_capability_warning"
)

// labels
const (
	ClientIDLabel  = "clientid"
	OutcomeLabel   = "outcome"
	ReasonLabel    = "reason"
	PartnerIDLabel = "partnerid"
	EndpointLabel  = "endpoint"
	MethodLabel    = "method"
	KindLabel      = "kind"
)

// The reasons of the warnings the checks raise.  A would-reject records a
// failure a permissive mode let through; a rejected explains a refusal to
// the caller and is not counted as a warning.
const (
	warningWouldReject = "would-reject"
	warningRejected    = "rejected"
)

// The defaults for the most bytes of a value that came from a token or a
// request that are recorded.  A basic auth user name is whatever the caller
// sends, and a token's principal, partner and capabilities are whatever its
// issuer put there, each up to the size of the request headers, so a value is
// cut before it becomes a label or part of a log line.  inboundConfig sets
// them.
const (
	// defaultMaxValueLen applies to a single value: a principal, a partner or
	// a parameter name.
	defaultMaxValueLen = 256

	// defaultMaxWarningLen applies to a whole warning, which holds several
	// values.
	defaultMaxWarningLen = 1024
)

// limits is how long a value from a token or a request may be when it is
// recorded.  A field of zero means its default, so the zero value is usable.
type limits struct {
	maxValue   int
	maxWarning int
}

// newLimits builds limits from the configured lengths, where zero is the
// default.
func newLimits(maxValue, maxWarning int) (limits, error) {
	if maxValue < 0 {
		return limits{}, fmt.Errorf("`%s.maxValueLen` must not be negative: %d", inboundConfigKey, maxValue)
	}

	if maxWarning < 0 {
		return limits{}, fmt.Errorf("`%s.maxWarningLen` must not be negative: %d", inboundConfigKey, maxWarning)
	}

	return limits{maxValue: maxValue, maxWarning: maxWarning}, nil
}

// value makes a principal, a partner or a parameter name safe to record.
func (l limits) value(s string) string {
	if l.maxValue == 0 {
		return sanitize(s, defaultMaxValueLen)
	}

	return sanitize(s, l.maxValue)
}

// warning makes a warning's text safe to record.
func (l limits) warning(s string) string {
	if l.maxWarning == 0 {
		return sanitize(s, defaultMaxWarningLen)
	}

	return sanitize(s, l.maxWarning)
}

// label values
const (
	Accepted = "accepted"
	Rejected = "rejected"

	// EmptyLabelValue is recorded in place of an empty label value, so that
	// a series reads clearly.
	EmptyLabelValue = "empty"

	// Unauthenticated is recorded as the client of a rejected request whose
	// credentials were never verified, such as an unknown basic auth user.
	// Recording the caller's value would let any caller add series.
	Unauthenticated = "unauthenticated"

	TokenMissing = "token_not_found"
	//nolint:gosec // a metric label, not a credential
	TokenTypeMismatch = "token_type_mismatch"

	WRPPIDMissing  = "wrp_pid_missing"
	WRPPIDMismatch = "wrp_pid_mismatch"
	WRPPIDMatch    = "wrp_pid_match"

	JWTPIDWildcard = "jwt_pid_wildcard"
	JWTPIDInvalid  = "jwt_pid_invalid"
)

// Auth related label values
const (
	// reasons
	UnknownReason       = "unknown"
	AuthMissingClaims   = "missing_expected_claims"
	AuthInvalidClaims   = "invalid_claim_values"
	AuthInvalidCreds    = "invalid_creds"
	AuthCannotVerify    = "could_not_verify_message"
	AuthInvalidScheme   = "invalid_scheme"
	AuthUnknownScheme   = "unknown_scheme"
	AuthEmptyPrincipal  = "empty_principal"
	AuthUnsatifiedExp   = "exp_not_satisfied"
	AuthUnsatifiedIAT   = "iat_not_satisfied"
	AuthUnsatifiedNBF   = "nbf_not_satisfied"
	AuthKeyNotFind      = "jwk_key_not_found"
	AuthBadCreds        = "bad_creds"
	AuthMissingCreds    = "missing_creds"
	NoCapabilitiesMatch = "no_capabilities_match"
	NoCIDRCapability    = "no_cidr_capability"
	OriginNotAllowed    = "origin_not_allowed"
	UnknownOrigin       = "unknown_origin"
	RateExceeded        = "rate_exceeded"
	ParameterNotAllowed = "parameter_not_allowed"
	TokenTooOld         = "token_too_old"
	NoIssuedAt          = "no_issued_at"
	RateDenied          = "rate_denied"
	// partners
	NonePartner     = "none"
	WildcardPartner = "wildcard"
	ManyPartner     = "many"
	// endpoints
	NoneEndpoint          = "no_endpoints"
	NotRecognizedEndpoint = "not_recognized"
)

type authenticatorEvent struct {
	limits
	l *zap.Logger

	// origin resolves where a request came from, for its log line.
	origin     *basculehttp.OriginResolver
	counter    *prometheus.CounterVec
	endpoints  []*regexp.Regexp
	parserOpts []jwt.ParseOption
}

func (ae authenticatorEvent) OnEvent(e bascule.AuthenticateEvent[*http.Request]) {
	if e.Err == nil {
		requestLogger(e.Source, ae.l).Debug("authenticator event: creds authenticated",
			append([]zap.Field{
				zap.String(PrincipalField, ae.value(principalOf(e.Token))),
				zap.String(PartnerField, ae.value(determinePartnerID(e.Token))),
			}, whereFrom(ae.origin, e.Source)...)...)

		return
	}

	ae.counter.With(ae.cleanLabels(ae.getLabels(e))).Add(1)
}

func (ae authenticatorEvent) getLabels(e bascule.AuthenticateEvent[*http.Request]) prometheus.Labels {
	var (
		client  string
		partner string
		reason  string
	)
	// A JWT rejected for its claims, e.g. an expired one, has no Token, but its
	// signature verified, so who it came from can be recorded.
	token := e.Token
	if token == nil {
		token = e.InvalidToken
	}

	if token != nil {
		client = token.Principal()
		partner = determinePartnerID(token)
	}

	if errors.Is(e.Err, basculejwt.ErrExpired) {
		reason = AuthUnsatifiedExp
	} else if errors.Is(e.Err, basculejwt.ErrInvalidIssuedAt) {
		reason = AuthUnsatifiedIAT
	} else if errors.Is(e.Err, basculejwt.ErrNotYetValid) {
		reason = AuthUnsatifiedNBF
	} else if errors.Is(e.Err, clortho.ErrKeyNotFound) {
		reason = AuthKeyNotFind
	} else if errors.Is(e.Err, basculejwt.ErrInvalidSignature) {
		reason = AuthCannotVerify
	} else if errors.Is(e.Err, bascule.ErrMissingCredentials) {
		reason = AuthMissingCreds
	} else if errors.Is(e.Err, bascule.ErrInvalidCredentials) {
		reason = AuthInvalidCreds
	} else if errors.Is(e.Err, errAuthEmptyPrincipal) {
		// the validator errors wrap bascule.ErrBadCredentials, so they are
		// checked before it
		reason = AuthEmptyPrincipal
	} else if errors.Is(e.Err, errAuthUnknownScheme) {
		reason = AuthUnknownScheme
	} else if errors.Is(e.Err, bascule.ErrBadCredentials) {
		reason = AuthBadCreds
	} else if _, ok := errors.AsType[*basculehttp.UnsupportedSchemeError](e.Err); ok {
		reason = AuthInvalidScheme
	} else {
		reason = UnknownReason
	}

	fs := []zap.Field{
		zap.String(PrincipalField, ae.value(client)),
		zap.String(PartnerField, ae.value(partner)),
		zap.String("jwt_rejection_reason", reason),
	}
	fs = append(fs, whereFrom(ae.origin, e.Source)...)

	l := requestLogger(e.Source, ae.l)
	if reason == UnknownReason {
		l.Error("authenticator event failure",
			append(fs, zap.Error(fmt.Errorf("unexpected event error: %v", e.Err)))...)
	} else {
		l.Info("authenticator event: creds rejected", fs...)
	}

	// A JWT token only exists once its signature has been verified, so its
	// client and partner are bounded by who holds a valid token.  A basic token
	// carries whatever user name the caller sent; its password is not checked
	// until authorization.
	if _, ok := token.(basculehttp.BasicToken); ok {
		client = Unauthenticated
	}

	return prometheus.Labels{
		ClientIDLabel:  client,
		PartnerIDLabel: partner,
		EndpointLabel:  determineEndpoint(ae.endpoints, e.Source),
		MethodLabel:    methodOf(e.Source),
		OutcomeLabel:   Rejected,
		ReasonLabel:    reason,
	}
}

type authorizerEvent struct {
	limits
	l *zap.Logger

	// origin resolves where a request came from, for its log lines.
	origin    *basculehttp.OriginResolver
	counter   *prometheus.CounterVec
	warnings  *prometheus.CounterVec
	endpoints []*regexp.Regexp
}

func (ae authorizerEvent) OnEvent(e bascule.AuthorizeEvent[*http.Request]) {
	var ls prometheus.Labels
	if e.Err == nil {
		ls = prometheus.Labels{
			ClientIDLabel:  principalOf(e.Token),
			PartnerIDLabel: determinePartnerID(e.Token),
			EndpointLabel:  determineEndpoint(ae.endpoints, e.Resource),
			MethodLabel:    methodOf(e.Resource),
			OutcomeLabel:   Accepted,
			ReasonLabel:    "",
		}
	} else {
		ls = ae.getLabels(e)
	}

	ae.counter.With(ae.cleanLabels(ls)).Add(1)
	ae.recordWarnings(e)
}

// recordWarnings counts and logs the capability warnings raised while
// authorizing a request.  A check in permissive mode lets a failing request
// through, so this is the only record that it would have been rejected.  The
// capability or origin a warning names is logged but kept out of the labels,
// since it comes from the token or the request.
func (ae authorizerEvent) recordWarnings(e bascule.AuthorizeEvent[*http.Request]) {
	if len(e.Warnings) == 0 {
		return
	}

	client := principalOf(e.Token)
	partner := determinePartnerID(e.Token)
	for _, w := range e.Warnings {
		// the explanation of a refusal is not a warning; the refusal is
		// already counted
		if w.Reason == warningRejected {
			continue
		}

		kind, reason := warningKindReason(w)
		ae.warnings.With(ae.cleanLabels(prometheus.Labels{
			KindLabel:     kind,
			ReasonLabel:   reason,
			ClientIDLabel: client,
		})).Add(1)

		requestLogger(e.Resource, ae.l).Info("authorizer event: capability warning",
			append([]zap.Field{
				zap.String(PrincipalField, ae.value(client)),
				zap.String(PartnerField, ae.value(partner)),
				zap.String("warning", ae.warning(w.String())),
			}, whereFrom(ae.origin, e.Resource)...)...)
	}
}

// warningKindReason extracts the capability kind and the reason of a warning
// for use as metric labels.  A warning that would have rejected the request
// carries its reason as an attribute; any other warning's reason is the
// warning itself, e.g. malformed.  Hyphens become underscores to match the
// other label values.
func warningKindReason(w bascule.Warning) (kind, reason string) {
	kind, reason = UnknownReason, w.Reason
	for _, attr := range w.Attrs {
		switch attr.Key {
		case KindLabel:
			kind = attr.Value
		case ReasonLabel:
			reason = attr.Value
		}
	}

	return strings.ReplaceAll(kind, "-", "_"), strings.ReplaceAll(reason, "-", "_")
}

func (ae authorizerEvent) getLabels(e bascule.AuthorizeEvent[*http.Request]) prometheus.Labels {
	client := ae.value(principalOf(e.Token))
	partner := ae.value(determinePartnerID(e.Token))
	fs := []zap.Field{
		zap.String("jwt_principal", client),
		zap.String("jwt_partner", partner),
	}
	// The claim errors wrap bascule.ErrBadCredentials, so they are checked
	// before it.
	var reason string
	if errors.Is(e.Err, errAuthMissingClaims) {
		reason = AuthMissingClaims
	} else if errors.Is(e.Err, errAuthInvalidClaims) {
		reason = AuthInvalidClaims
	} else if errors.Is(e.Err, bascule.ErrBadCredentials) {
		reason = AuthBadCreds
	} else if errors.Is(e.Err, basculehttp.ErrNoCIDRCapability) {
		reason = NoCIDRCapability
	} else if errors.Is(e.Err, basculehttp.ErrOriginNotAllowed) {
		reason = OriginNotAllowed
	} else if errors.Is(e.Err, basculehttp.ErrUnknownOrigin) {
		reason = UnknownOrigin
	} else if errors.Is(e.Err, errTokenTooOld) {
		reason = TokenTooOld
	} else if errors.Is(e.Err, errNoIssuedAt) {
		reason = NoIssuedAt
	} else if errors.Is(e.Err, bascule.ErrUnauthorized) {
		reason = NoCapabilitiesMatch
	} else {
		reason = UnknownReason
	}

	fs = append(fs, zap.String("jwt_rejection_reason", reason))
	fs = append(fs, whereFrom(ae.origin, e.Resource)...)

	l := requestLogger(e.Resource, ae.l)
	if reason == UnknownReason {
		l.Error("authorizer event failure",
			append(fs, zap.Error(fmt.Errorf("unexpected event error: %v", e.Err)))...)
	} else {
		l.Info("authorizer event: creds rejected", fs...)
	}

	// Basic credentials are checked while authorizing, so a bad_creds
	// rejection of one carries a user name that was never verified.
	if _, ok := e.Token.(basculehttp.BasicToken); ok && reason == AuthBadCreds {
		client = Unauthenticated
	}

	return prometheus.Labels{
		ClientIDLabel:  client,
		PartnerIDLabel: partner,
		EndpointLabel:  determineEndpoint(ae.endpoints, e.Resource),
		MethodLabel:    methodOf(e.Resource),
		OutcomeLabel:   Rejected,
		ReasonLabel:    reason,
	}
}

// unlimited is the limit for a value that is cleaned but not cut.
const unlimited = math.MaxInt

// sanitize makes s safe to record: invalid UTF-8 is replaced, since Prometheus
// panics on it, and then s is cut to limit bytes on a character boundary.  A
// cut value ends with an ellipsis.  The value is cleaned first because
// replacing invalid bytes can make it longer.
func sanitize(s string, limit int) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if len(s) <= limit {
		return s
	}

	// cut on a character boundary, without running off the front of bytes that
	// are not text at all
	n := limit
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n] + "..."
}

// cleanLabels makes ls safe and clear to record.  Prometheus panics on a label
// value that is not valid UTF-8, and some values, such as a basic auth user
// name, are whatever the caller sends, so invalid bytes are replaced.  The
// client and partner come from a token or a request, so they are also cut to
// the maximum value length; every other label is set by this service, and
// keeps its length.  An empty value becomes EmptyLabelValue.
func (l limits) cleanLabels(ls prometheus.Labels) prometheus.Labels {
	for k, v := range ls {
		if k == ClientIDLabel || k == PartnerIDLabel {
			v = l.value(v)
		} else {
			v = sanitize(v, unlimited)
		}

		if v == "" {
			v = EmptyLabelValue
		}
		ls[k] = v
	}

	return ls
}

// principalOf returns the token's principal, or "" without a token.  bascule
// always sets an event's token and request, but a listener must not panic on
// the request goroutine if one is missing; the event is still counted.
func principalOf(token bascule.Token) string {
	if token == nil {
		return ""
	}

	return token.Principal()
}

// methodOf returns the request's method, or "" without a request.
func methodOf(req *http.Request) string {
	if req == nil {
		return ""
	}

	return req.Method
}

// determineEndpoint names the bucket a request falls in, for the endpoint
// metric label.  A bucket pattern is matched the way a capability's URL
// pattern is: against the path with the /api/vN prefix removed, with and
// without its leading slash, and only from the start.  So a bucket may be
// written as a capability is, "device/.*/config\\b", or with the leading
// slash, or with the API prefix.
func determineEndpoint(endpoints []*regexp.Regexp, req *http.Request) string {
	if len(endpoints) == 0 || req == nil {
		return NoneEndpoint
	}

	full := req.URL.EscapedPath()
	normalized := stripAPIVersion(*req.URL)
	stripped := normalized.EscapedPath()
	candidates := []string{full, stripped}
	if unrooted, ok := strings.CutPrefix(stripped, "/"); ok && !strings.HasPrefix(unrooted, "/") {
		candidates = append(candidates, unrooted)
	}

	for _, r := range endpoints {
		for _, candidate := range candidates {
			if idxs := r.FindStringIndex(candidate); len(idxs) > 0 && idxs[0] == 0 {
				return strings.ReplaceAll(r.String(), " ", "_")
			}
		}
	}

	return NotRecognizedEndpoint
}

func determinePartnerID(token bascule.Token) string {
	switch token.(type) {
	case basculehttp.BasicToken, nil:
		return ""
	case bascule.AttributesAccessor:
	default:
		return AuthUnknownScheme
	}

	partnerIDsVal, ok := bascule.GetAttribute[any](token.(bascule.AttributesAccessor), PartnerKeys...)
	if !ok {
		return AuthMissingClaims
	}

	ids, err := cast.ToStringSliceE(partnerIDsVal)
	if err != nil {
		return AuthInvalidClaims
	}

	if len(ids) == 0 {
		return NonePartner
	}

	if len(ids) == 1 {
		if ids[0] == Wildcard {
			return WildcardPartner
		}

		return ids[0]
	}

	if slices.Contains(ids, Wildcard) {
		return WildcardPartner
	}

	return ManyPartner
}
