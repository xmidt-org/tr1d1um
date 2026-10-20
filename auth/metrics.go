// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

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

// label values
const (
	Accepted = "accepted"
	Rejected = "rejected"

	// EmptyLabelValue is recorded in place of an empty label value, so that
	// a series reads clearly.
	EmptyLabelValue = "empty"

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
	l          *zap.Logger
	counter    *prometheus.CounterVec
	endpoints  []*regexp.Regexp
	parserOpts []jwt.ParseOption
}

func (ae authenticatorEvent) OnEvent(e bascule.AuthenticateEvent[*http.Request]) {
	if e.Err == nil {
		ae.l.Debug("authenticator event: creds authenticated")

		return
	}

	ae.counter.With(cleanLabels(ae.getLabels(e))).Add(1)
}

func (ae authenticatorEvent) getLabels(e bascule.AuthenticateEvent[*http.Request]) prometheus.Labels {
	var (
		client  string
		partner string
		reason  string
	)
	if e.Token != nil {
		client = e.Token.Principal()
		partner = determinePartnerID(e.Token)
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

	fs := []zap.Field{zap.String("sat_client_id", client), zap.String("sat_partner_id", partner), zap.String("sat_rejection_reason", reason)}
	if reason == UnknownReason {
		ae.l.Error("authenticator event failure", append(fs, zap.Error(fmt.Errorf("unexpected event error: %v", e.Err)))...)
	} else {
		ae.l.Info("authenticator event: creds rejected", fs...)
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
	l         *zap.Logger
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

	ae.counter.With(cleanLabels(ls)).Add(1)
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
		ae.warnings.With(cleanLabels(prometheus.Labels{
			KindLabel:     kind,
			ReasonLabel:   reason,
			ClientIDLabel: client,
		})).Add(1)

		ae.l.Info("authorizer event: capability warning",
			zap.String("sat_client_id", client),
			zap.String("sat_partner_id", partner),
			zap.String("warning", w.String()))
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
	client := principalOf(e.Token)
	partner := determinePartnerID(e.Token)
	fs := []zap.Field{zap.String("sat_client_id", client), zap.String("sat_partner_id", partner)}
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

	fs = append(fs, zap.String("sat_rejection_reason", reason))
	if reason == UnknownReason {
		ae.l.Error("authorizer event failure", append(fs, zap.Error(fmt.Errorf("unexpected event error: %v", e.Err)))...)
	} else {
		ae.l.Info("authorizer event: creds rejected", fs...)
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

// cleanLabels makes ls safe and clear to record.  Prometheus panics on a label
// value that is not valid UTF-8, and some values, such as a basic auth user
// name, are whatever the caller sends, so invalid bytes are replaced.  An
// empty value becomes EmptyLabelValue.
func cleanLabels(ls prometheus.Labels) prometheus.Labels {
	for k, v := range ls {
		v = strings.ToValidUTF8(v, "\uFFFD")
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

func determineEndpoint(endpoints []*regexp.Regexp, req *http.Request) string {
	if len(endpoints) == 0 || req == nil {
		return NoneEndpoint
	}

	url := req.URL.EscapedPath()
	for _, r := range endpoints {
		idxs := r.FindStringIndex(url)
		if len(idxs) == 0 {
			continue
		} else if idxs[0] != 0 {
			continue
		}

		return strings.ReplaceAll(r.String(), " ", "_")
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
