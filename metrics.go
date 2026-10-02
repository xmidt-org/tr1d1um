// SPDX-FileCopyrightText: 2025 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/xmidt-org/touchstone"
	"go.uber.org/fx"
)

const (
	// metric names
	serviceConfigsRetriesCounter = "service_configs_retries"

	// metric labels
	apiLabel = "api"

	// metric label values
	// api
	stat_api   = "stat"
	device_api = "device"
)

func provideMetrics() fx.Option {
	return fx.Options(
		touchstone.CounterVec(
			prometheus.CounterOpts{
				Name: serviceConfigsRetriesCounter,
				Help: "Count of retries for xmidt service configs api calls.",
			},
			[]string{apiLabel}...,
		),
		touchstone.CounterVec(
			prometheus.CounterOpts{
				Name: capabilityCheckCounter,
				Help: "Outcome of capability checks by label, outcome, and endpoint bucket.",
			},
			[]string{capabilityLabelName, outcomeLabel, endpointLabel}...,
		),
		touchstone.CounterVec(
			prometheus.CounterOpts{
				Name: authOutcomeCounter,
				Help: "Outcome of authentication attempts by scheme.",
			},
			[]string{schemeLabel, outcomeLabel}...,
		),
		touchstone.CounterVec(
			prometheus.CounterOpts{
				Name: partnerIDsCounter,
				Help: "Where a request's WRP partner IDs were sourced from.",
			},
			[]string{sourceLabel}...,
		),
	)
}
