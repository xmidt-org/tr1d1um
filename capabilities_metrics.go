// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"net/http"
	"regexp"

	"github.com/prometheus/client_golang/prometheus"
)

const (
	// capabilityCheckCounter counts the outcome of every capability check.
	// Watching it per label is how a deployment knows when it is safe to move
	// a label from monitor to enforce.
	capabilityCheckCounter = "capability_check"

	// metric labels
	capabilityLabelName = "label"
	outcomeLabel        = "outcome"
	endpointLabel       = "endpoint"

	// outcome values
	outcomeAllowed   = "allowed"
	outcomeRejected  = "rejected"
	outcomeMonitored = "monitored"

	// endpointUnbucketed is the endpoint label for a request matching no
	// configured bucket.  Requests are grouped into buckets rather than
	// labeled by raw path, which would be unbounded cardinality.
	endpointUnbucketed = "other"
)

const (
	// authOutcomeCounter counts the result of every authentication attempt.
	// Without it a wrong password, an unsupported scheme and an unverifiable
	// token are all equally invisible.
	authOutcomeCounter = "auth_outcome"

	// metric labels
	schemeLabel = "scheme"

	// scheme values.  The scheme is taken from the Authorization header rather
	// than from the token, so a failed parse is still attributable.
	schemeBearer = "bearer"
	schemeBasic  = "basic"
	schemeNone   = "none"
	schemeOther  = "other"

	// outcome values shared with the capability counter
	outcomeSuccess = "success"
	outcomeFailure = "failure"
)

const (
	// partnerIDsCounter records where a request's partner IDs came from.
	// Watching the header source drain to zero is how a deployment knows it
	// can leave partnerIDs.allowHeader false.
	partnerIDsCounter = "partner_ids"

	// metric labels
	sourceLabel = "source"
)

// capabilityMetric records capability check outcomes.
type capabilityMetric interface {
	record(label, outcome string, r *http.Request)
}

// nopCapabilityMetric is used when no counter has been provided.
type nopCapabilityMetric struct{}

func (nopCapabilityMetric) record(string, string, *http.Request) {}

// bucketedCapabilityMetric groups requests by endpoint bucket before counting.
type bucketedCapabilityMetric struct {
	counter *prometheus.CounterVec
	buckets []*regexp.Regexp
}

// newCapabilityMetric compiles the configured endpoint buckets.  An unusable
// bucket pattern is an error so it is caught at startup.
func newCapabilityMetric(counter *prometheus.CounterVec, endpointBuckets []string) (capabilityMetric, error) {
	if counter == nil {
		return nopCapabilityMetric{}, nil
	}

	buckets := make([]*regexp.Regexp, 0, len(endpointBuckets))
	for _, b := range endpointBuckets {
		re, err := regexp.Compile(b)
		if err != nil {
			return nil, err
		}
		buckets = append(buckets, re)
	}

	return &bucketedCapabilityMetric{counter: counter, buckets: buckets}, nil
}

func (m *bucketedCapabilityMetric) record(label, outcome string, r *http.Request) {
	m.counter.With(prometheus.Labels{
		capabilityLabelName: label,
		outcomeLabel:        outcome,
		endpointLabel:       m.bucket(r),
	}).Inc()
}

// bucket returns the first configured bucket matching the request path.
func (m *bucketedCapabilityMetric) bucket(r *http.Request) string {
	path := r.URL.EscapedPath()
	for _, re := range m.buckets {
		if match := re.FindString(path); match != "" {
			return match
		}
	}
	return endpointUnbucketed
}
