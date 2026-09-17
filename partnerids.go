// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import "github.com/prometheus/client_golang/prometheus"

// partnerIDConfig controls the fallback source for partner IDs.
//
// A partner ID is stated on every outbound WRP message.  tr1d1um cannot tell
// which partner owns the target device, so it does not authorize the choice --
// the device confirms or rejects it.  What tr1d1um can insist on is that the
// statement comes from the verified token whenever the token makes one.
type partnerIDConfig struct {
	// AllowHeader lets the X-Webpa-Partner-Id header supply partner IDs for
	// callers whose token states none, such as basic-auth credentials.
	//
	// It defaults to false: a request that presents no partner IDs from any
	// permitted source is refused rather than sent downstream unscoped.
	AllowHeader bool `mapstructure:"allowHeader"`

	// AllowEmpty lets a request proceed with no partner IDs at all, leaving
	// the outbound WRP message unscoped for the device to interpret.
	//
	// It defaults to false, so such a request is refused instead.  Enable it
	// only where the devices being reached accept an unscoped request.
	AllowEmpty bool `mapstructure:"allowEmpty"`
}

// partnerIDRecorder returns the callback that counts where a request's partner
// IDs came from.
func partnerIDRecorder(counter *prometheus.CounterVec) func(string) {
	if counter == nil {
		return nil
	}

	return func(source string) {
		counter.With(prometheus.Labels{sourceLabel: source}).Inc()
	}
}
