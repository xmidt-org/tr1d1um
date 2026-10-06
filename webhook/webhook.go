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
	webhookConfigKey  = "webhook"
	clientConfigKey   = "webhook.client"
	listenerConfigKey = "webhook.listener"
)

type clientConfig struct {
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

type listnerConfig struct {
	PullInterval string
}

func NewListenerOpts(cfg listnerConfig) (chrysom.ListenerOptions, error) {
	opts := chrysom.ListenerOptions{
		// Defaults
		chrysom.PullInterval(0), // 5s default
	}

	if len(cfg.PullInterval) != 0 {
		t, err := time.ParseDuration(cfg.PullInterval)
		if err != nil {
			return nil, err
		}

		opts = append(opts, chrysom.PullInterval(t))
	}

	return opts, nil
}
func NewClientOpts(cfg clientConfig, tracing candlelight.Tracing, auth auth.Decorator) chrysom.ClientOptions {
	return chrysom.ClientOptions{
		chrysom.Bucket(cfg.Bucket),
		chrysom.StoreBaseURL(cfg.StoreBaseURL),
		chrysom.StoreAPIPath(cfg.StoreAPIPath),
		chrysom.GetClientLogger(sallust.Get),
		chrysom.HTTPClient(NewHTTPClient(cfg, tracing)),
		chrysom.Auth(auth),
	}
}

func NewHTTPClient(cfg clientConfig, tracing candlelight.Tracing) *http.Client {
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
