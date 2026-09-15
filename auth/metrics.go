// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"

	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/cast"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/clortho"

	// nolint: staticcheck

	"go.uber.org/zap"
)

const Wildcard = "*"

// Names for our metrics
const (
	AuthCapabilityCheckCount = "auth_capability_check"
)

// labels
const (
	ClientIDLabel  = "clientid"
	OutcomeLabel   = "outcome"
	ReasonLabel    = "reason"
	PartnerIDLabel = "partnerid"
	EndpointLabel  = "endpoint"
	MethodLabel    = "method"
)

// label values
const (
	Accepted = "accepted"
	Rejected = "rejected"

	TokenMissing = "token_not_found"
	// nolint: gosec
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
	// partners
	NonePartner     = "none"
	WildcardPartner = "wildcard"
	ManyPartner     = "many"
	// endpoints
	NoneEndpoint          = "no_endpoints"
	NotRecognizedEndpoint = "not_recognized"
)

var (
	errEventMetricMetadata = errors.New("could not parse jwt for additional metric metdata")
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
	} else if e.Source == nil {
		panic(fmt.Errorf("authenticator event handling failure: expected event source to be a non-nil: %v", e.Err))
	}

	ae.counter.With(ae.getLabels(e)).Add(1)
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
	} else if scheme, _, err := basculehttp.ParseAuthorization(e.Source.Header.Get(basculehttp.DefaultAuthorizationHeader)); err == nil &&
		scheme == basculehttp.SchemeBearer {
		reparseFailureMsg := "authenticator event: failed to reparse the request auth"
		opts := append([]jwt.ParseOption{jwt.WithResetValidators(true),
			jwt.WithValidator(jwt.IsIssuedAtValid()),
			jwt.WithValidator(jwt.IsNbfValid())},
			ae.parserOpts...)
		if parser, err := basculejwt.NewTokenParser(opts...); err != nil {
			ae.l.Error(reparseFailureMsg, zap.Error(errors.Join(errEventMetricMetadata, err)))
		} else if authValue := e.Source.Header.Get(basculehttp.DefaultAuthorizationHeader); len(authValue) == 0 {
			ae.l.Error(reparseFailureMsg, zap.Error(errors.Join(errEventMetricMetadata, bascule.ErrMissingCredentials)))
		} else if _, value, err := basculehttp.ParseAuthorization(authValue); err != nil {
			ae.l.Error(reparseFailureMsg, zap.Error(errors.Join(errEventMetricMetadata, err)))
		} else if t, err := parser.Parse(context.Background(), value); err == nil {
			client = t.Principal()
			partner = determinePartnerID(t)
		} else {
			ae.l.Debug(reparseFailureMsg, zap.Error(errors.Join(errEventMetricMetadata, err)))
		}
	}

	if errors.Is(e.Err, jwt.TokenExpiredError{}) {
		reason = AuthUnsatifiedExp
	} else if errors.Is(e.Err, jwt.InvalidIssuedAtError{}) {
		reason = AuthUnsatifiedIAT
	} else if errors.Is(e.Err, jwt.TokenNotYetValidError{}) {
		reason = AuthUnsatifiedNBF
	} else if errors.Is(e.Err, clortho.ErrKeyProviderKeyNotFound) {
		reason = AuthKeyNotFind
	} else if errors.Is(e.Err, jws.VerifyError()) {
		reason = AuthCannotVerify
	} else if errors.Is(e.Err, bascule.ErrMissingCredentials) {
		reason = AuthMissingCreds
	} else if errors.Is(e.Err, bascule.ErrInvalidCredentials) {
		reason = AuthInvalidCreds
	} else if errors.Is(e.Err, bascule.ErrBadCredentials) {
		reason = AuthBadCreds
	} else if _, ok := errors.AsType[*basculehttp.UnsupportedSchemeError](e.Err); ok {
		reason = AuthInvalidScheme
	} else if errors.Is(e.Err, errAuthEmptyPrincipal) {
		reason = AuthEmptyPrincipal
	} else if errors.Is(e.Err, errAuthUnknownScheme) {
		reason = AuthUnknownScheme
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
		MethodLabel:    e.Source.Method,
		OutcomeLabel:   Rejected,
		ReasonLabel:    reason,
	}
}

type authorizerEvent struct {
	l         *zap.Logger
	counter   *prometheus.CounterVec
	endpoints []*regexp.Regexp
}

func (ae authorizerEvent) OnEvent(e bascule.AuthorizeEvent[*http.Request]) {
	var ls prometheus.Labels
	if e.Err == nil {
		ls = prometheus.Labels{
			ClientIDLabel:  e.Token.Principal(),
			PartnerIDLabel: determinePartnerID(e.Token),
			EndpointLabel:  determineEndpoint(ae.endpoints, e.Resource),
			MethodLabel:    e.Resource.Method,
			OutcomeLabel:   Accepted,
			ReasonLabel:    "",
		}
	} else if e.Token == nil {
		panic(fmt.Errorf("authorizer event handling failure: expected event token to be a non-nil since it passed the `authenticator` middleware without issue: %v", e.Err))
	} else if e.Resource == nil {
		panic(fmt.Errorf("authorizer event handling failure: expected event resource to be a non-nil: %v", e.Err))
	} else {
		ls = ae.getLabels(e)
	}

	ae.counter.With(ls).Add(1)
}

func (ae authorizerEvent) getLabels(e bascule.AuthorizeEvent[*http.Request]) prometheus.Labels {
	client := e.Token.Principal()
	partner := determinePartnerID(e.Token)
	fs := []zap.Field{zap.String("sat_client_id", client), zap.String("sat_partner_id", partner)}
	reason := ""
	if errors.Is(e.Err, bascule.ErrBadCredentials) {
		reason = AuthBadCreds
	} else if errors.Is(e.Err, bascule.ErrUnauthorized) {
		reason = NoCapabilitiesMatch
	} else if errors.Is(e.Err, errAuthMissingClaims) {
		reason = AuthMissingClaims
	} else if errors.Is(e.Err, errAuthInvalidClaims) {
		reason = AuthInvalidClaims
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
		MethodLabel:    e.Resource.Method,
		OutcomeLabel:   Rejected,
		ReasonLabel:    reason,
	}
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
