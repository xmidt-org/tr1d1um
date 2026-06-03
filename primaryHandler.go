// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
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
	webhook "github.com/xmidt-org/webhook-schema"
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

type authAcquirerConfig struct {
	JWT   transaction.RemoteBearerTokenAcquirerOptions
	Basic string
}

type provideWebhookHandlersIn struct {
	fx.In
	Lifecycle          fx.Lifecycle
	V                  *viper.Viper
	WebhookConfig      ancla.Config
	ArgusClientTimeout httpClientTimeout `name:"argus_client_timeout"`
	Logger             *zap.Logger
	Tracing            candlelight.Tracing
	Tf                 *touchstone.Factory
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

func createAuthAcquirer(config authAcquirerConfig) (transaction.AuthAcquirer, error) {
	if config.JWT.AuthURL != "" && config.JWT.Buffer != 0 && config.JWT.Timeout != 0 {
		return &transaction.JwtAcquirer{Config: config.JWT}, nil
	}

	if config.Basic != "" {
		return &transaction.BasicAcquirer{Token: config.Basic}, nil
	}

	return nil, errors.New("auth acquirer not configured properly")
}

func v2WebhookValidators(c ancla.Config) (webhook.Validators, error) {
	//build validators and webhook handler for previous version that only check loopback.

	v2Validation := c.Validation
	v2Validation.URL.AllowLoopback = true
	v2Validation.IP.Allow = true
	v2Validation.Domain.AllowSpecialUseDomains = true

	return buildWebhookValidators(v2Validation)
}

func buildWebhookValidators(validation schema.SchemaURLValidatorConfig) (webhook.Validators, error) {
	if validation.TTL.Now == nil {
		validation.TTL.Now = time.Now
	}

	checker, err := validation.BuildURLChecker()
	if err != nil {
		return nil, err
	}

	return webhook.Validators(validation.BuildOptions(checker)), nil
}

func provideWebhookHandlers(in provideWebhookHandlersIn) (out provideWebhookHandlersOut, err error) {
	if !in.V.IsSet(webhookConfigKey) {
		in.Logger.Info("Webhook service disabled")
		return
	}

	service := &simpleWebhookService{
		logger: in.Logger,
	}

	builtValidators, err := buildWebhookValidators(in.WebhookConfig.Validation)
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook validators: %w", err)
	}

	v2Validators, err := v2WebhookValidators(in.WebhookConfig)
	if err != nil {
		return out, fmt.Errorf("failed to setup v2 webhook validators: %w", err)
	}

	handlerConfig := ancla.HandlerConfig{
		V:                 builtValidators,
		DisablePartnerIDs: in.WebhookConfig.DisablePartnerIDs,
		GetLogger:         sallust.Get,
	}

	out.AddWebhookHandler = ancla.NewAddWRPEventStreamHandler(service, handlerConfig)
	out.GetAllWebhooksHandler = ancla.NewGetAllWRPEventStreamsHandler(service, handlerConfig)

	v2HandlerConfig := handlerConfig
	v2HandlerConfig.V = v2Validators
	out.V2AddWebhookHandler = ancla.NewAddWRPEventStreamHandler(service, v2HandlerConfig)

	in.Logger.Info("Webhook service enabled")
	return
}

// simpleWebhookService provides a basic implementation of ancla.Service
type simpleWebhookService struct {
	logger *zap.Logger
	store  []schema.Manifest // Simple in-memory store for demo
}

// Add implements ancla.Service interface
func (s *simpleWebhookService) Add(ctx context.Context, owner string, manifest schema.Manifest) error {
	s.logger.Info("Adding webhook", zap.String("owner", owner))
	s.store = append(s.store, manifest)
	return nil
}

// GetAll implements ancla.Service interface
func (s *simpleWebhookService) GetAll(ctx context.Context) ([]schema.Manifest, error) {
	s.logger.Info("Getting all webhooks")
	result := make([]schema.Manifest, len(s.store))
	copy(result, s.store)
	return result, nil
}

func provideHandlers() fx.Option {
	return fx.Options(
		arrange.ProvideKey(authAcquirerKey, authAcquirerConfig{}),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
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
