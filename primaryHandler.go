// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	gokitprometheus "github.com/go-kit/kit/metrics/prometheus"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
	"github.com/xmidt-org/webhook-schema"
	"github.com/xmidt-org/webpa-common/v2/xhttp"
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
			arrange.UnmarshalKey("prometheus", touchstone.Config{}),
			arrange.UnmarshalKey("prometheus.handler", touchhttp.Config{}),
			provideWebhookHandlers,
		),
	)
}

func provideServiceOptions(in ServiceOptionsIn) (ServiceOptionsOut, error) {
	var errs error

	xmidtHTTPClient := newHTTPClient(in.XmidtClientTimeout, in.Tracing)
	stat_retries_counter, err := in.ServiceConfigsRetries.CurryWith(prometheus.Labels{apiLabel: stat_api})
	errs = errors.Join(errs, err)
	// Stat Service configs
	statOptions := &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   in.Logger,
						Retries:  in.RequestMaxRetries,
						Interval: in.RequestRetryInterval,
						Counter:  gokitprometheus.NewCounter(stat_retries_counter),
					},
					xmidtHTTPClient.Do),
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", in.TargetURL),
	}

	device_retries_counter, err := in.ServiceConfigsRetries.CurryWith(prometheus.Labels{apiLabel: device_api})
	errs = errors.Join(errs, err)
	// WRP Service configs
	translationOptions := &translation.ServiceOptions{
		XmidtWrpURL: fmt.Sprintf("%s/device", in.TargetURL),
		WRPSource:   in.WRPSource,
		T: transaction.New(
			&transaction.Options{
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   in.Logger,
						Retries:  in.RequestMaxRetries,
						Interval: in.RequestRetryInterval,
						Counter:  gokitprometheus.NewCounter(device_retries_counter),
					},
					xmidtHTTPClient.Do),
			}),
	}

	return ServiceOptionsOut{
		StatServiceOptions:        statOptions,
		TranslationServiceOptions: translationOptions,
	}, errs
}
