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
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/arrange/arrangehttp"
	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/translation"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type PrimaryEndpointIn struct {
	fx.In
	V                           *viper.Viper
	Router                      *mux.Router  `name:"server_primary"`
	APIRouter                   *mux.Router  `name:"api_router"`
	AuthChain                   *alice.Chain `name:"auth_chain"`
	Tracing                     candlelight.Tracing
	Logger                      *zap.Logger
	StatServiceOptions          *stat.ServiceOptions
	TranslationOptions          *translation.ServiceOptions
	Acquirer                    acquire.Acquirer
	ReducedLoggingResponseCodes []int    `name:"reducedLoggingResponseCodes"`
	TranslationServices         []string `name:"supportedServices"`
}

type handleWebhookRoutesIn struct {
	fx.In
	Logger                *zap.Logger
	APIRouter             *mux.Router  `name:"api_router"`
	AuthChain             *alice.Chain `name:"auth_chain"`
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
}

type APIRouterIn struct {
	fx.In
	PrevVerSupport bool `name:"previousVersionSupport"`
}

type PrimaryMMIn struct {
	fx.In
	Primary alice.Chain `name:"middleware_primary_metrics"`
}

type HealthMMIn struct {
	fx.In
	Health alice.Chain `name:"middleware_health_metrics"`
}

type MetricMiddlewareOut struct {
	fx.Out
	Primary alice.Chain `name:"middleware_primary_metrics"`
	Health  alice.Chain `name:"middleware_health_metrics"`
}

func provideServers() fx.Option {
	return fx.Options(
		arrange.ProvideKey(reqMaxRetriesKey, 0),
		arrange.ProvideKey(reqRetryIntervalKey, time.Duration(0)),
		arrange.ProvideKey("previousVersionSupport", true),
		arrange.ProvideKey("targetURL", ""),
		arrange.ProvideKey("WRPSource", ""),
		arrange.ProvideKey(translationServicesKey, []string{}),
		fx.Provide(metricMiddleware),
		fx.Provide(
			fx.Annotated{
				Name:   "reducedLoggingResponseCodes",
				Target: arrange.UnmarshalKey(reducedTransactionLoggingCodesKey, []int{}),
			},
			provideStatServiceOptions,
			provideTranslationOptions,
			fx.Annotated{
				Name:   "api_router",
				Target: provideAPIRouter,
			},
		),
		arrangehttp.Server{
			Name: "server_primary",
			Key:  "primary",
			Inject: arrange.Inject{
				PrimaryMMIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_health",
			Key:  "health",
			Inject: arrange.Inject{
				HealthMMIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_pprof",
			Key:  "pprof",
		}.Provide(),
		arrangehttp.Server{
			Name: "server_metrics",
			Key:  "metric",
		}.Provide(),
		fx.Invoke(
			handlePrimaryEndpoint,
		),
	)
}

func handlePrimaryEndpoint(in PrimaryEndpointIn) {
	otelMuxOptions := []otelmux.Option{
		otelmux.WithPropagators(in.Tracing.Propagator()),
		otelmux.WithTracerProvider(in.Tracing.TracerProvider()),
	}

	in.Router.Use(otelmux.Middleware("mainSpan", otelMuxOptions...),
		candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator()),
	)

	if in.V.IsSet(authAcquirerKey) {
		acquirer := in.Acquirer
		in.TranslationOptions.AuthAcquirer = acquirer
		in.StatServiceOptions.AuthAcquirer = acquirer
		in.Logger.Info("Outbound request authentication token acquirer enabled")
	}
	ss := stat.NewService(in.StatServiceOptions)
	ts := translation.NewService(in.TranslationOptions)

	// Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes).
	stat.ConfigHandler(&stat.Options{
		S:                           ss,
		APIRouter:                   in.APIRouter,
		Authenticate:                in.AuthChain,
		Log:                         in.Logger,
		ReducedLoggingResponseCodes: in.ReducedLoggingResponseCodes,
	})
	translation.ConfigHandler(&translation.Options{
		S:                           ts,
		APIRouter:                   in.APIRouter,
		Authenticate:                in.AuthChain,
		Log:                         in.Logger,
		ValidServices:               in.TranslationServices,
		ReducedLoggingResponseCodes: in.ReducedLoggingResponseCodes,
	})
}

func handleWebhookRoutes(in handleWebhookRoutesIn) {
	if in.AddWebhookHandler != nil && in.GetAllWebhooksHandler != nil {
		in.APIRouter.Handle("/hook", in.AuthChain.Then(in.AddWebhookHandler)).Methods(http.MethodPost)
		in.APIRouter.Handle("/hooks", in.AuthChain.Then(in.GetAllWebhooksHandler)).Methods(http.MethodGet)
	}
}

func metricMiddleware(bundle touchhttp.ServerBundle) (out MetricMiddlewareOut) {
	out.Primary = alice.New(bundle.ForServer("server_primary").Then)
	out.Health = alice.New(bundle.ForServer("server_health").Then)
	return
}

func provideAPIRouter(in APIRouterIn) *mux.Router {
	rootRouter := mux.NewRouter()
	// if we want to support the previous API version, then include it in the
	// api base.
	urlPrefix := fmt.Sprintf("/%s/", apiBase)
	if in.PrevVerSupport {
		urlPrefix = fmt.Sprintf("/%s/", apiBaseDualVersion)
	}
	APIRouter := rootRouter.PathPrefix(urlPrefix).Subrouter()
	return APIRouter
}
