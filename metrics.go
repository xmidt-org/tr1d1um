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
	return touchstone.CounterVec(
		prometheus.CounterOpts{
			Name: serviceConfigsRetriesCounter,
			Help: "Count of retries for xmidt service configs api calls.",
		},
		[]string{apiLabel}...,
	)
}
