// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package tr1d1um

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/chrysom"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/internal/stat"
	"github.com/xmidt-org/tr1d1um/internal/transaction"
	"github.com/xmidt-org/tr1d1um/internal/translation"
	webhook "github.com/xmidt-org/webhook-schema"
	"github.com/xmidt-org/webpa-common/v2/xhttp"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// HttpClientTimeout contains timeouts for an HTTP client and its requests.
type HttpClientTimeout struct {
	// ClientTimeout is HTTP Client Timeout.
	ClientTimeout time.Duration

	// RequestTimeout can be imposed as an additional timeout on the request
	// using context cancellation.
	RequestTimeout time.Duration

	// NetDialerTimeout is the net dialer timeout
	NetDialerTimeout time.Duration
}

type authAcquirerConfig struct {
	JWT   acquire.RemoteBearerTokenAcquirerOptions
	Basic string
}

type provideWebhookHandlersIn struct {
	fx.In
	Lifecycle     fx.Lifecycle
	V             *viper.Viper
	WebhookConfig ancla.Config
	Logger        *zap.Logger
	Tracing       candlelight.Tracing
	Tf            *touchstone.Factory
	AnclaSvc      *ancla.ClientService
}

type provideAnclaHTTPClientIn struct {
	fx.In

	ArgusClientTimeout HttpClientTimeout `name:"argus_client_timeout"`
	Tracing            candlelight.Tracing
}

type provideWebhookHandlersOut struct {
	fx.Out
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	V2AddWebhookHandler   http.Handler `name:"v2_add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
}

type ServiceOptionsIn struct {
	fx.In
	Logger               *zap.Logger
	XmidtClientTimeout   HttpClientTimeout `name:"xmidt_client_timeout"`
	RequestMaxRetries    int               `name:"requestMaxRetries"`
	RequestRetryInterval time.Duration     `name:"requestRetryInterval"`
	TargetURL            string            `name:"targetURL"`
	WRPSource            string            `name:"WRPSource"`
	Tracing              candlelight.Tracing
}

type ServiceOptionsOut struct {
	fx.Out
	StatServiceOptions        *stat.ServiceOptions
	TranslationServiceOptions *translation.ServiceOptions
}

func ProvideAnclaHTTPClient(in provideAnclaHTTPClientIn) chrysom.HTTPClient {
	return newHTTPClient(in.ArgusClientTimeout, in.Tracing)
}

func newHTTPClient(timeouts HttpClientTimeout, tracing candlelight.Tracing) *http.Client {
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

func createAuthAcquirer(config authAcquirerConfig) (acquire.Acquirer, error) {
	if config.JWT.AuthURL != "" && config.JWT.Buffer != 0 && config.JWT.Timeout != 0 {
		return acquire.NewRemoteBearerTokenAcquirer(config.JWT)
	}

	if config.Basic != "" {
		return acquire.NewFixedAuthAcquirer(config.Basic)
	}

	return nil, errors.New("auth acquirer not configured properly")
}

func v2WebhookValidators(c ancla.Config) ([]webhook.Option, error) {
	checker, err := ancla.BuildURLChecker(c.Validation)
	if err != nil {
		return nil, err
	}

	return c.Validation.BuildOptions(checker), nil
}

func provideWebhookHandlers(in provideWebhookHandlersIn) (out provideWebhookHandlersOut, err error) {
	// Webhooks (if not configured, handlers are not set up)
	if !in.V.IsSet(anclaClientConfigKey) {
		in.Logger.Info("Webhook service disabled")
		return
	}

	out.GetAllWebhooksHandler = ancla.NewGetAllWebhooksHandler(in.AnclaSvc, ancla.HandlerConfig{})

	checker, err := ancla.BuildURLChecker(in.WebhookConfig.Validation)
	if err != nil {
		return out, fmt.Errorf("failed to set up url checker for validation: %s", err)
	}

	out.AddWebhookHandler = ancla.NewAddWebhookHandler(in.AnclaSvc, ancla.HandlerConfig{
		V:                 in.WebhookConfig.Validation.BuildOptions(checker),
		DisablePartnerIDs: in.WebhookConfig.DisablePartnerIDs,
	})
	v2Validators, err := v2WebhookValidators(in.WebhookConfig)
	if err != nil {
		return out, fmt.Errorf("failed to setup v2 webhook validators: %s", err)
	}

	out.V2AddWebhookHandler = ancla.NewAddWebhookHandler(in.AnclaSvc, ancla.HandlerConfig{
		V:                 v2Validators,
		DisablePartnerIDs: in.WebhookConfig.DisablePartnerIDs,
	})

	in.Logger.Info("Webhook service enabled")
	return
}

func ProvideHandlers() fx.Option {
	return fx.Options(
		arrange.ProvideKey(authAcquirerKey, authAcquirerConfig{}),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
			arrange.UnmarshalKey(anclaClientConfigKey, chrysom.BasicClientConfig{}),
			arrange.UnmarshalKey("prometheus", touchstone.Config{}),
			arrange.UnmarshalKey("prometheus.handler", touchhttp.Config{}),
			provideWebhookHandlers,
		),
	)
}

func provideServiceOptions(in ServiceOptionsIn) ServiceOptionsOut {
	xmidtHTTPClient := newHTTPClient(in.XmidtClientTimeout, in.Tracing)
	// Stat Service configs
	statOptions := &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   in.Logger,
						Retries:  in.RequestMaxRetries,
						Interval: in.RequestRetryInterval,
					},
					xmidtHTTPClient.Do),
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", in.TargetURL),
	}

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
					},
					xmidtHTTPClient.Do),
			}),
	}

	return ServiceOptionsOut{
		StatServiceOptions:        statOptions,
		TranslationServiceOptions: translationOptions,
	}
}
