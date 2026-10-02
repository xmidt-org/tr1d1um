// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"strings"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/xmidt-org/bascule"
)

// newAuthOutcomeListener returns a bascule listener that counts the result of
// every authentication attempt, by the scheme the caller presented.
//
// The scheme comes from the Authorization header rather than from the resulting
// token, so an attempt that failed to parse is still attributable: a run of
// basic failures looks like credential guessing, whereas the same count spread
// across schemes looks like a misconfigured client.
func newAuthOutcomeListener(counter *prometheus.CounterVec) bascule.ListenerFunc[bascule.AuthenticateEvent[*http.Request]] {
	if counter == nil {
		return func(bascule.AuthenticateEvent[*http.Request]) {}
	}

	return func(e bascule.AuthenticateEvent[*http.Request]) {
		scheme, outcome := authOutcomeOf(e)
		counter.With(prometheus.Labels{
			schemeLabel:  scheme,
			outcomeLabel: outcome,
		}).Inc()
	}
}

// authOutcomeOf reduces an authentication event to the labels it is counted
// under.  Kept separate from the counter so the mapping can be exercised
// without a metrics registry.
func authOutcomeOf(e bascule.AuthenticateEvent[*http.Request]) (scheme, outcome string) {
	outcome = outcomeSuccess
	if e.Err != nil {
		outcome = outcomeFailure
	}

	return schemeOf(e.Source), outcome
}

// schemeOf reports the Authorization scheme a request presented.  Unrecognized
// schemes collapse to a single value so a caller cannot inflate metric
// cardinality by inventing them.
func schemeOf(r *http.Request) string {
	if r == nil {
		return schemeNone
	}

	value := r.Header.Get("Authorization")
	if value == "" {
		return schemeNone
	}

	scheme, _, found := strings.Cut(value, " ")
	if !found {
		return schemeOther
	}

	switch strings.ToLower(scheme) {
	case schemeBearer:
		return schemeBearer
	case schemeBasic:
		return schemeBasic
	default:
		return schemeOther
	}
}
