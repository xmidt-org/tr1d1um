// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"net"
	"net/http"
	"time"

	"github.com/xmidt-org/ancla/chrysom"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/auth"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	webhookConfigKey = "webhook"
	clientConfigKey  = "webhook.client"
)

type config struct {
	// StoreBaseURL is the Argus URL (i.e. https://example-argus.io:8090)
	StoreBaseURL string

	// StoreAPIPath sets the store url api path.
	StoreAPIPath string

	// Bucket partition to be used by this client.
	Bucket string

	// ClientTimeout is HTTP Client Timeout.
	ClientTimeout time.Duration

	// NetDialerTimeout is the net dialer timeout
	NetDialerTimeout time.Duration
}

func NewClientOpts(cfg config, tracing candlelight.Tracing, auth auth.Decorator) chrysom.ClientOptions {
	return chrysom.ClientOptions{
		chrysom.Bucket(cfg.Bucket),
		chrysom.StoreBaseURL(cfg.StoreBaseURL),
		chrysom.StoreAPIPath(cfg.StoreAPIPath),
		chrysom.GetClientLogger(sallust.Get),
		chrysom.HTTPClient(NewHTTPClient(cfg, tracing)),
		chrysom.Auth(auth),
	}
}

func NewHTTPClient(cfg config, tracing candlelight.Tracing) *http.Client {
	if cfg.ClientTimeout <= 0 {
		cfg.ClientTimeout = time.Second * 50
	}
	if cfg.NetDialerTimeout <= 0 {
		cfg.NetDialerTimeout = time.Second * 5
	}

	var transport http.RoundTripper = &http.Transport{
		Dial: (&net.Dialer{
			Timeout: cfg.NetDialerTimeout,
		}).Dial,
	}
	transport = otelhttp.NewTransport(transport,
		otelhttp.WithPropagators(tracing.Propagator()),
		otelhttp.WithTracerProvider(tracing.TracerProvider()),
	)

	return &http.Client{
		Timeout:   cfg.ClientTimeout,
		Transport: transport,
	}
}
