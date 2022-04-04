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
	"github.com/xmidt-org/bascule/key"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
	"github.com/xmidt-org/webpa-common/v2/logging"
	"github.com/xmidt-org/webpa-common/v2/xhttp"
	"github.com/xmidt-org/webpa-common/v2/xmetrics"
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
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory `json:"keys"`

	// Leeway is used to set the amount of time buffer should be given to JWT
	// time values, such as nbf
	Leeway bascule.Leeway
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

type createAuthAcquirerIn struct {
	fx.In
	AuthAcquirer authAcquirerConfig `name:"authAcquirer"`
}

func createAuthAcquirer(in createAuthAcquirerIn) (acquire.Acquirer, error) {
	if in.AuthAcquirer.JWT.AuthURL != "" && in.AuthAcquirer.JWT.Buffer != 0 && in.AuthAcquirer.JWT.Timeout != 0 {
		return acquire.NewRemoteBearerTokenAcquirer(in.AuthAcquirer.JWT)
	}

	if in.AuthAcquirer.Basic != "" {
		return acquire.NewFixedAuthAcquirer(in.AuthAcquirer.Basic)
	}

	return nil, errors.New("auth acquirer not configured properly")
}

type provideWebhookHandlersIn struct {
	fx.In
	V                  viper.Viper
	WebhookConfigKey   ancla.Config
	ArgusClientTimeout httpClientTimeout `name:"argus_client_timeout"`
	Logger             *zap.Logger
	MetricsRegistry    xmetrics.Registry
	Tracing            candlelight.Tracing
}

type provideWebhookHandlersOut struct {
	fx.Out
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
}

func provideWebhookHandlers(in provideWebhookHandlersIn) (out provideWebhookHandlersOut, err error) {
	// Webhooks (if not configured, handlers are not set up)
	if !in.V.IsSet(webhookConfigKey) {
		in.Logger.Info("Webhook service disabled")
		return
	}
	webhookConfig := in.WebhookConfigKey

	webhookConfig.Logger = gokitLogger(in.Logger)
	webhookConfig.MetricsProvider = in.MetricsRegistry
	webhookConfig.Argus.HTTPClient = newHTTPClient(in.ArgusClientTimeout, in.Tracing)

	svc, _, err := ancla.Initialize(webhookConfig, getLogger, logging.WithLogger)
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook service: %s", err)
	}

	builtValidators, err := ancla.BuildValidators(webhookConfig.Validation)
	if err != nil {
		return out, fmt.Errorf("failed to initialize webhook validators: %s", err)
	}

	out.AddWebhookHandler = ancla.NewAddWebhookHandler(svc, ancla.HandlerConfig{
		MetricsProvider:   in.MetricsRegistry,
		V:                 builtValidators,
		DisablePartnerIDs: webhookConfig.DisablePartnerIDs,
		GetLogger:         getLogger,
	})

	out.GetAllWebhooksHandler = ancla.NewGetAllWebhooksHandler(svc, ancla.HandlerConfig{
		GetLogger: getLogger,
	})
	in.Logger.Info("Webhook service enabled")
	return
}

func provideHandlers() fx.Option {
	return fx.Options(
		arrange.ProvideKey(authAcquirerKey, authAcquirerConfig{}),
		arrange.ProvideKey("authHeader", []string{}),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
			arrange.UnmarshalKey("jwtValidator", JWTValidator{}),
			arrange.UnmarshalKey("capabilityCheck", CapabilityConfig{}),
			createAuthAcquirer,
			provideWebhookHandlers,
		),
		fx.Invoke(handleWebhookRoutes),
	)
}

type ServiceConfigIn struct {
	fx.In
	Logger               *zap.Logger
	XmidtHTTPClient      *http.Client      `name:"xmidt_http_client"`
	XmidtClientTimeout   httpClientTimeout `name:"xmidt_client_timeout"`
	RequestMaxRetries    int               `name:"requestMaxRetries"`
	RequestRetryInterval time.Duration     `name:"requestRetryInterval"`
	TargetURL            string            `name:"targetURL"`
	WRPSource            string            `name:"WRPSource"`
}

func provideStatServiceOptions(in ServiceConfigIn) *stat.ServiceOptions {
	//
	// Stat Service configs
	//
	return &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   gokitLogger(in.Logger),
						Retries:  in.RequestMaxRetries,
						Interval: in.RequestRetryInterval,
					},
					in.XmidtHTTPClient.Do),
				RequestTimeout: in.XmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", in.TargetURL),
	}
}

func provideTranslationOptions(in ServiceConfigIn) *translation.ServiceOptions {
	//
	// WRP Service configs
	//
	return &translation.ServiceOptions{
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
					in.XmidtHTTPClient.Do),
			}),
	}
}
