// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/tr1d1um/translation"
)

func TestSchemeOf(t *testing.T) {
	tests := []struct {
		description string
		header      string
		noRequest   bool
		expected    string
	}{
		{description: "no request", noRequest: true, expected: schemeNone},
		{description: "no header", header: "", expected: schemeNone},
		{description: "bearer", header: "Bearer abc.def.ghi", expected: schemeBearer},
		{description: "basic", header: "Basic dXNlcjpwYXNz", expected: schemeBasic},
		{description: "scheme is case insensitive", header: "BEARER abc", expected: schemeBearer},
		{description: "unknown scheme collapses", header: "Negotiate abc", expected: schemeOther},
		{description: "no space collapses", header: "Bearerabc", expected: schemeOther},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			var r *http.Request
			if !tc.noRequest {
				r = httptest.NewRequest(http.MethodGet, "/api/v3/device/mac:1/config", nil)
				if tc.header != "" {
					r.Header.Set("Authorization", tc.header)
				}
			}

			assert.Equal(t, tc.expected, schemeOf(r))
		})
	}
}

func TestAuthOutcomeOf(t *testing.T) {
	tests := []struct {
		description     string
		header          string
		err             error
		expectedScheme  string
		expectedOutcome string
	}{
		{
			description:     "successful bearer",
			header:          "Bearer abc",
			expectedScheme:  schemeBearer,
			expectedOutcome: outcomeSuccess,
		}, {
			description:     "failed bearer",
			header:          "Bearer abc",
			err:             bascule.ErrInvalidCredentials,
			expectedScheme:  schemeBearer,
			expectedOutcome: outcomeFailure,
		}, {
			description:     "failed basic",
			header:          "Basic abc",
			err:             bascule.ErrBadCredentials,
			expectedScheme:  schemeBasic,
			expectedOutcome: outcomeFailure,
		}, {
			description:     "missing credentials",
			err:             bascule.ErrMissingCredentials,
			expectedScheme:  schemeNone,
			expectedOutcome: outcomeFailure,
		}, {
			description:     "unsupported scheme is still attributable",
			header:          "Negotiate abc",
			err:             errors.New("unsupported"),
			expectedScheme:  schemeOther,
			expectedOutcome: outcomeFailure,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/api/v3/device/mac:1/config", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}

			scheme, outcome := authOutcomeOf(
				bascule.AuthenticateEvent[*http.Request]{Source: r, Err: tc.err})

			assert.Equal(t, tc.expectedScheme, scheme)
			assert.Equal(t, tc.expectedOutcome, outcome)
		})
	}
}

// The listener must tolerate having no counter, which is how it is built when
// metrics are unavailable.
func TestNewAuthOutcomeListener(t *testing.T) {
	tests := []struct {
		description string
		counter     *prometheus.CounterVec
	}{
		{
			description: "no counter",
		}, {
			description: "with a counter",
			counter: prometheus.NewCounterVec(
				prometheus.CounterOpts{Name: authOutcomeCounter, Help: "test"},
				[]string{schemeLabel, outcomeLabel}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			listener := newAuthOutcomeListener(tc.counter)
			require.NotNil(t, listener)

			r := httptest.NewRequest(http.MethodGet, "/api/v3/device/mac:1/config", nil)
			r.Header.Set("Authorization", "Bearer abc")

			assert.NotPanics(t, func() {
				listener(bascule.AuthenticateEvent[*http.Request]{Source: r})
			})
		})
	}
}

func TestPartnerIDRecorder(t *testing.T) {
	t.Run("nil counter yields no recorder", func(t *testing.T) {
		assert.Nil(t, partnerIDRecorder(nil))
	})

	tests := []struct {
		description string
		source      string
	}{
		{description: "token", source: translation.PartnerIDSourceToken},
		{description: "header", source: translation.PartnerIDSourceHeader},
		{description: "empty", source: translation.PartnerIDSourceEmpty},
		{description: "refused", source: translation.PartnerIDSourceRefused},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			counter := prometheus.NewCounterVec(
				prometheus.CounterOpts{Name: partnerIDsCounter, Help: "test"},
				[]string{sourceLabel})

			record := partnerIDRecorder(counter)
			require.NotNil(t, record)

			assert.NotPanics(t, func() { record(tc.source) })
		})
	}
}
