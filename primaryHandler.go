// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/retry"
	"github.com/xmidt-org/retry/retryhttp"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/internal/viperfx"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
	"github.com/xmidt-org/webhook-schema"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// httpClientTimeout contains timeouts for an HTTP client and its requests.
type httpClientTimeout struct {
	// ClientTimeout is HTTP Client Timeout.
	ClientTimeout time.Duration

	// RequestTimeout can be imposed as an additional timeout on the request
	// using context cancellation.
	RequestTimeout time.Duration

	// NetDialerTimeout is the net dialer timeout
	NetDialerTimeout time.Duration
}

type provideWebhookHandlersIn struct {
	fx.In
	Lifecycle     fx.Lifecycle
	V             *viper.Viper
	WebhookConfig ancla.Config
	Logger        *zap.Logger
	Service       ancla.Service
	Tracing       candlelight.Tracing
	Tf            *touchstone.Factory
}

type provideWebhookHandlersOut struct {
	fx.Out
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	V2AddWebhookHandler   http.Handler `name:"v2_add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
}

type ServiceOptionsIn struct {
	fx.In
	Logger                *zap.Logger
	XmidtClientTimeout    httpClientTimeout      `name:"xmidt_client_timeout"`
	RequestMaxRetries     int                    `name:"requestMaxRetries"`
	RequestRetryInterval  time.Duration          `name:"requestRetryInterval"`
	TargetURL             string                 `name:"targetURL"`
	WRPSource             string                 `name:"WRPSource"`
	ServiceConfigsRetries *prometheus.CounterVec `name:"service_configs_retries"`

	Tracing candlelight.Tracing
}

type ServiceOptionsOut struct {
	fx.Out
	StatServiceOptions        *stat.ServiceOptions
	TranslationServiceOptions *translation.ServiceOptions
}

func newHTTPClient(timeouts httpClientTimeout, tracing candlelight.Tracing) *http.Client {
	var transport http.RoundTripper = &http.Transport{
		Dial: (&net.Dialer{
			Timeout: timeouts.NetDialerTimeout,
		}).Dial,
	}
	transport = otelhttp.NewTransport(transport,
		otelhttp.WithPropagators(tracing.Propagator()),
		otelhttp.WithTracerProvider(tracing.TracerProvider()),
	)

	return &http.Client{
		Timeout:   timeouts.ClientTimeout,
		Transport: transport,
	}
}

// defaultRetryInterval is the time between retries when none is configured.
const defaultRetryInterval = time.Second

// newRetryingClient wraps client so that a request failing with a temporary
// error is retried up to retries times, interval apart, counting each retry
// with counter.  When retries is less than 1, requests are not retried.
//
// CleanupResponse closes the body of every response but the last, which goes
// to the caller to close.
//
//nolint:bodyclose
func newRetryingClient(logger *zap.Logger, retries int, interval time.Duration, counter prometheus.Counter, client *http.Client) (retryhttp.HTTPClient, error) {
	if retries < 1 {
		return client, nil
	}
	if interval <= 0 {
		interval = defaultRetryInterval
	}

	runner, err := retry.NewRunner(
		retry.WithPolicyFactory[*http.Response](retry.Config{
			Interval:   interval,
			MaxRetries: retries,
		}),
		retry.WithShouldRetry(func(_ *http.Response, err error) bool {
			var temp interface{ Temporary() bool }
			return errors.As(err, &temp) && temp.Temporary()
		}),
		retry.WithOnAttempt(
			retryhttp.CleanupResponse,
			func(a retry.Attempt[*http.Response]) {
				switch {
				case !a.Done():
					counter.Inc()
					logger.Debug("retrying HTTP transaction", zap.Error(a.Err), zap.Int("retry", a.Retries+1))
				case a.Err != nil:
					logger.Error("All HTTP transaction retries failed", zap.Error(a.Err), zap.Int("retries", a.Retries))
				}
			},
		),
	)
	if err != nil {
		return nil, err
	}

	rc, err := retryhttp.NewClient(
		retryhttp.WithHTTPClient(client),
		retryhttp.WithRunner(runner),
	)
	if err != nil {
		return nil, err
	}

	return rc, nil
}

func v2WebhookValidators(c ancla.Config) ([]webhook.Option, error) {
	return (&schema.SchemaURLValidatorConfig{
		URL: schema.URLVConfig{
			AllowLoopback: c.Validation.URL.AllowLoopback,
		},
		Domain: schema.DomainVConfig{
			AllowSpecialUseDomains: true,
		},
		IP: schema.IPVConfig{
			Allow: true,
		},
		TTL: c.Validation.TTL,
	}).BuildOptions()
}

func provideWebhookHandlers(in provideWebhookHandlersIn) (out provideWebhookHandlersOut, err error) {
	// Webhooks (if not configured, handlers are not set up)
	if !in.V.IsSet(webhookConfigKey) {
		in.Logger.Info("Webhook service disabled")
		return
	}
	out.GetAllWebhooksHandler = ancla.NewGetAllWRPEventStreamsHandler(in.Service, ancla.HandlerConfig{
		GetLogger: sallust.Get,
	})

	builtValidators, err := in.WebhookConfig.Validation.BuildOptions()
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook validators: %w", err)
	}

	out.AddWebhookHandler = ancla.NewAddWRPEventStreamHandler(in.Service, ancla.HandlerConfig{
		V:                 builtValidators,
		DisablePartnerIDs: in.WebhookConfig.DisablePartnerIDs,
		GetLogger:         sallust.Get,
	})

	v2Validators, err := v2WebhookValidators(in.WebhookConfig)
	if err != nil {
		return out, fmt.Errorf("failed to setup v2 webhook validators: %w", err)
	}

	out.V2AddWebhookHandler = ancla.NewAddWRPEventStreamHandler(in.Service, ancla.HandlerConfig{
		V:                 v2Validators,
		DisablePartnerIDs: in.WebhookConfig.DisablePartnerIDs,
		GetLogger:         sallust.Get,
	})

	in.Logger.Info("Webhook service enabled")
	return
}

func provideHandlers() fx.Option {
	return fx.Options(
		fx.Provide(
			viperfx.Unmarshal("prometheus", touchstone.Config{}),
			viperfx.Unmarshal("prometheus.handler", touchhttp.Config{}),
			provideWebhookHandlers,
		),
	)
}

func provideServiceOptions(in ServiceOptionsIn) (ServiceOptionsOut, error) {
	var errs error

	xmidtHTTPClient := newHTTPClient(in.XmidtClientTimeout, in.Tracing)
	stat_retries_counter, err := in.ServiceConfigsRetries.GetMetricWith(prometheus.Labels{apiLabel: stat_api})
	errs = errors.Join(errs, err)
	statClient, err := newRetryingClient(in.Logger, in.RequestMaxRetries, in.RequestRetryInterval, stat_retries_counter, xmidtHTTPClient)
	errs = errors.Join(errs, err)
	// Stat Service configs
	statOptions := &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do:             statClient.Do,
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", in.TargetURL),
	}

	device_retries_counter, err := in.ServiceConfigsRetries.GetMetricWith(prometheus.Labels{apiLabel: device_api})
	errs = errors.Join(errs, err)
	deviceClient, err := newRetryingClient(in.Logger, in.RequestMaxRetries, in.RequestRetryInterval, device_retries_counter, xmidtHTTPClient)
	errs = errors.Join(errs, err)
	// WRP Service configs
	translationOptions := &translation.ServiceOptions{
		XmidtWrpURL: fmt.Sprintf("%s/device", in.TargetURL),
		WRPSource:   in.WRPSource,
		T: transaction.New(
			&transaction.Options{
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
				Do:             deviceClient.Do,
			}),
	}

	return ServiceOptionsOut{
		StatServiceOptions:        statOptions,
		TranslationServiceOptions: translationOptions,
	}, errs
}
