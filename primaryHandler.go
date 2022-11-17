/**
 * Copyright 2022 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/clortho"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
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
	JWT   acquire.RemoteBearerTokenAcquirerOptions
	Basic string
}

type CapabilityConfig struct {
	Type            string
	Prefix          string
	AcceptAllMethod string
	EndpointBuckets []string
}

// JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// Config is used to create the clortho Resolver & Refresher for JWT verification keys
	Config clortho.Config

	// Leeway is used to set the amount of time buffer should be given to JWT
	// time values, such as nbf
	Leeway bascule.Leeway
}

type provideWebhookHandlersIn struct {
	fx.In
	V                  *viper.Viper
	WebhookConfigKey   ancla.Config
	ArgusClientTimeout httpClientTimeout `name:"argus_client_timeout"`
	Logger             *zap.Logger
	Measures           *ancla.Measures
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
	Logger               *zap.Logger
	XmidtClientTimeout   httpClientTimeout `name:"xmidt_client_timeout"`
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

func createAuthAcquirer(config authAcquirerConfig) (acquire.Acquirer, error) {
	if config.JWT.AuthURL != "" && config.JWT.Buffer != 0 && config.JWT.Timeout != 0 {
		return acquire.NewRemoteBearerTokenAcquirer(config.JWT)
	}

	if config.Basic != "" {
		return acquire.NewFixedAuthAcquirer(config.Basic)
	}

	return nil, errors.New("auth acquirer not configured properly")
}

func v2WebhookValidators(c ancla.Config) (ancla.Validators, error) {
	//build validators and webhook handler for previous version that only check loopback.
	v, err := ancla.BuildValidators(ancla.ValidatorConfig{
		URL: ancla.URLVConfig{
			AllowLoopback:        c.Validation.URL.AllowLoopback,
			AllowIP:              true,
			AllowSpecialUseHosts: true,
			AllowSpecialUseIPs:   true,
		},
		TTL: c.Validation.TTL,
	})
	if err != nil {
		return ancla.Validators{}, err
	}

	return v, nil
}

func provideWebhookHandlers(in provideWebhookHandlersIn) (out provideWebhookHandlersOut, err error) {
	// Webhooks (if not configured, handlers are not set up)
	if !in.V.IsSet(webhookConfigKey) {
		in.Logger.Info("Webhook service disabled")
		return
	}

	webhookConfig := in.WebhookConfigKey
	webhookConfig.Logger = in.Logger
	listenerMeasures := ancla.ListenerConfig{
		Measures: *in.Measures,
	}
	webhookConfig.BasicClientConfig.HTTPClient = newHTTPClient(in.ArgusClientTimeout, in.Tracing)

	svc, err := ancla.NewService(webhookConfig, sallust.Get)
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook service: %s", err)
	}

	stopWatches, err := svc.StartListener(listenerMeasures, sallust.With)
	if err != nil {
		return out, fmt.Errorf("webhook service start listener error: %s", err)
	}
	in.Logger.Info("Webhook service enabled")

	defer stopWatches()

	out.GetAllWebhooksHandler = ancla.NewGetAllWebhooksHandler(svc, ancla.HandlerConfig{
		GetLogger: sallust.Get,
	})

	builtValidators, err := ancla.BuildValidators(webhookConfig.Validation)
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook validators: %s", err)
	}

	out.AddWebhookHandler = ancla.NewAddWebhookHandler(svc, ancla.HandlerConfig{
		V:                 builtValidators,
		DisablePartnerIDs: webhookConfig.DisablePartnerIDs,
		GetLogger:         sallust.Get,
	})

	v, err := v2WebhookValidators(webhookConfig)
	if err != nil {
		return out, fmt.Errorf("failed to setup v2 webhook validators: %s", err)
	}

	out.V2AddWebhookHandler = ancla.NewAddWebhookHandler(svc, ancla.HandlerConfig{
		V:                 v,
		DisablePartnerIDs: webhookConfig.DisablePartnerIDs,
		GetLogger:         sallust.Get,
	})

	in.Logger.Info("Webhook service enabled")
	return
}

func provideHandlers() fx.Option {
	return fx.Options(
		arrange.ProvideKey(authAcquirerKey, authAcquirerConfig{}),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
			arrange.UnmarshalKey("jwtValidator", JWTValidator{}),
			arrange.UnmarshalKey("capabilityCheck", CapabilityConfig{}),
			arrange.UnmarshalKey("prometheus", touchstone.Config{}),
			arrange.UnmarshalKey("prometheus.handler", touchhttp.Config{}),
			func(c JWTValidator) clortho.Config {
				return c.Config
			},
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
						Logger:   gokitLogger(in.Logger),
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
						Logger:   gokitLogger(in.Logger),
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
