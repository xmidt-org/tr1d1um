// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"net/http"
	"net/netip"
	"sync"

	"github.com/justinas/alice"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/sallust"
)

// The request logger fields that describe the caller.  Every line logged
// with the request logger after the auth chain carries them, the response
// line included, so a request can be traced back to who made it, from where,
// and what they were held to.
const (
	// PrincipalField is the token's principal: the sub claim of a JWT or the
	// user name of a basic credential.
	PrincipalField = "jwt_principal"

	// PartnerField is the partner the token names, as determinePartnerID
	// summarizes it.
	PartnerField = "jwt_partner"

	// RemoteAddrField is the address of the peer that opened the
	// connection, as the server saw it.
	RemoteAddrField = "remoteAddr"

	// OriginField is the caller origin: the peer, or the nearest hop in the
	// forwarding headers that is not a trusted proxy.  It is what the CIDR
	// check judges.  The forwarding headers themselves are in
	// requestHeaders.
	OriginField = "origin"

	// CIDRField lists the networks the CIDR check held the caller to, from
	// their token or an override.  An empty list is an unrestricted caller.
	// It is absent when the check is not configured.
	CIDRField = "cidr"

	// RateField lists the rates the rate check held the caller to, from
	// their token, an override or the defaults.  An empty list is an
	// unrestricted caller.  It is absent when the check is not configured.
	RateField = "rate"

	// ParamField lists the parameter capabilities, without their prefix,
	// the parameter check held the caller to, from their token or an
	// override.  An empty list is an unrestricted caller.  It is absent when
	// the check is not configured or the route has no parameters to check.
	ParamField = "tr181"
)

// callerRecord collects what bascule learns about a caller while it runs, so
// that the request logger can carry it afterwards.  An approver sees only the
// context, which cannot be changed from inside one, so the record is a
// pointer put in the context before bascule runs and filled in as it goes.
type callerRecord struct {
	lock sync.Mutex

	// networks are the networks the CIDR check held the caller to, and set
	// is whether the check ran.
	networks []netip.Prefix
	set      bool
}

type callerRecordKey struct{}

// withCallerRecord gives the request a callerRecord for the checks to fill
// in.
func withCallerRecord(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := context.WithValue(request.Context(), callerRecordKey{}, &callerRecord{})
		next.ServeHTTP(response, request.WithContext(ctx))
	})
}

// recordFrom returns the request's callerRecord, or nil outside the chain.
func recordFrom(ctx context.Context) *callerRecord {
	r, _ := ctx.Value(callerRecordKey{}).(*callerRecord)
	return r
}

// setNetworks records the networks the CIDR check held the caller to.  It is
// safe on a nil record, which is what a resolver called outside the chain
// gets.
func (r *callerRecord) setNetworks(networks []netip.Prefix) {
	if r == nil {
		return
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	r.networks = networks
	r.set = true
}

// heldTo returns the networks the CIDR check held the caller to, as text, and
// whether the check ran.
func (r *callerRecord) heldTo() (networks []string, ok bool) {
	if r == nil {
		return nil, false
	}

	r.lock.Lock()
	defer r.lock.Unlock()

	if !r.set {
		return nil, false
	}

	networks = make([]string, 0, len(r.networks))
	for _, n := range r.networks {
		networks = append(networks, n.String())
	}

	return networks, true
}

// addLogFields adds fields to the request logger, so that every later line
// logged with it carries them.
func addLogFields(ctx context.Context, fields ...zap.Field) context.Context {
	return sallust.With(ctx, sallust.Get(ctx).With(fields...))
}

// requestLogger returns the logger a request's lines are written with, which
// carries the request's URL, method, headers and trace, or fallback when
// there is no request or it has no logger.  bascule's listeners use it so
// that what they log about a request sits beside the request.
func requestLogger(r *http.Request, fallback *zap.Logger) *zap.Logger {
	if r == nil {
		return fallback
	}

	return sallust.GetDefault(r.Context(), fallback)
}

// logCaller adds the caller to the request logger once bascule has accepted
// them: who they are, where they called from, and the networks the CIDR
// check held them to.  The rate and parameter checks add their own fields
// when they run, since they run later.
func logCaller(resolver *basculehttp.OriginResolver, lim limits) alice.Constructor {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
			var principal, partner string
			if token, ok := bascule.Get(request.Context()); ok {
				principal = token.Principal()
				partner = determinePartnerID(token)
			}

			fields := append([]zap.Field{
				zap.String(PrincipalField, lim.value(principal)),
				zap.String(PartnerField, lim.value(partner)),
			}, whereFrom(resolver, request)...)

			ctx := addLogFields(request.Context(), fields...)
			next.ServeHTTP(response, request.WithContext(ctx))
		})
	}
}

// whereFrom returns the fields that say where a request came from: the peer,
// the origin the resolver finds, and the networks the CIDR check held the
// caller to, if it has run.  A nil resolver trusts no proxies.  There is
// nothing to say without a request.
func whereFrom(resolver *basculehttp.OriginResolver, request *http.Request) []zap.Field {
	if request == nil {
		return nil
	}

	if resolver == nil {
		resolver = &basculehttp.OriginResolver{}
	}

	fields := []zap.Field{zap.String(RemoteAddrField, request.RemoteAddr)}
	if origin, ok := resolver.Origin(request); ok {
		fields = append(fields, zap.Stringer(OriginField, origin))
	}

	if networks, ok := recordFrom(request.Context()).heldTo(); ok {
		fields = append(fields, zap.Strings(CIDRField, networks))
	}

	return fields
}
