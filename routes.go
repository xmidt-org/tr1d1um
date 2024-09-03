// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/arrange/arrangehttp"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/httpaux"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/sallust/sallusthttp"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/translation"
	webhook "github.com/xmidt-org/webhook-schema"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	errFailedWebhookUnmarshal = errors.New("failed to JSON unmarshal webhook")

	v2WarningHeader = "X-Xmidt-Warning"
)

type primaryEndpointIn struct {
	fx.In
	V                           *viper.Viper
	Router                      *mux.Router `name:"server_primary"`
	APIRouter                   *mux.Router `name:"api_router"`
	AuthChain                   alice.Chain `name:"auth_chain"`
	Tracing                     candlelight.Tracing
	Logger                      *zap.Logger
	StatServiceOptions          *stat.ServiceOptions
	TranslationOptions          *translation.ServiceOptions
	AuthAcquirer                authAcquirerConfig `name:"authAcquirer"`
	ReducedLoggingResponseCodes []int              `name:"reducedLoggingResponseCodes"`
	TranslationServices         []string           `name:"supportedServices"`
}

type handleWebhookRoutesIn struct {
	fx.In
	Logger  *zap.Logger
	Tracing candlelight.Tracing

	APIRouter             *mux.Router  `name:"api_router"`
	AuthChain             alice.Chain  `name:"auth_chain"`
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	V2AddWebhookHandler   http.Handler `name:"v2_add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
	WebhookConfig         ancla.Config
	PreviousVersion       bool `name:"previousVersionSupport"`
}

type apiAltRouterIn struct {
	fx.In
	APIRouter       *mux.Router `name:"api_router"`
	AlternateRouter *mux.Router `name:"server_alternate"`
	URLPrefix       string      `name:"url_prefix"`
}

type apiRouterIn struct {
	fx.In
	PrimaryRouter *mux.Router `name:"server_primary"`
	URLPrefix     string      `name:"url_prefix"`
}

type provideURLPrefixIn struct {
	fx.In
	PrevVerSupport bool `name:"previousVersionSupport"`
}

type primaryMetricMiddlewareIn struct {
	fx.In
	Primary alice.Chain `name:"middleware_primary_metrics"`
}

type alternateMetricMiddlewareIn struct {
	fx.In
	Alternate alice.Chain `name:"middleware_alternate_metrics"`
}

type healthMetricMiddlewareIn struct {
	fx.In
	Health alice.Chain `name:"middleware_health_metrics"`
}

type metricMiddlewareOut struct {
	fx.Out
	Primary   alice.Chain `name:"middleware_primary_metrics"`
	Alternate alice.Chain `name:"middleware_alternate_metrics"`
	Health    alice.Chain `name:"middleware_health_metrics"`
}

type metricsRoutesIn struct {
	fx.In
	Router  *mux.Router `name:"server_metrics"`
	Handler touchhttp.Handler
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
			fx.Annotated{
				Name:   "api_router",
				Target: provideAPIRouter,
			},
			fx.Annotated{
				Name:   "url_prefix",
				Target: provideURLPrefix,
			},
			provideServiceOptions,
		),
		arrangehttp.Server{
			Name: "server_primary",
			Key:  "servers.primary",
			Inject: arrange.Inject{
				primaryMetricMiddlewareIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_alternate",
			Key:  "servers.alternate",
			Inject: arrange.Inject{
				alternateMetricMiddlewareIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_health",
			Key:  "servers.health",
			Inject: arrange.Inject{
				healthMetricMiddlewareIn{},
			},
			Invoke: arrange.Invoke{
				func(r *mux.Router) {
					r.Handle("/health", httpaux.ConstantHandler{
						StatusCode: http.StatusOK,
					}).Methods("GET")
				},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_pprof",
			Key:  "servers.pprof",
		}.Provide(),
		arrangehttp.Server{
			Name: "server_metrics",
			Key:  "servers.metrics",
		}.Provide(),
		fx.Invoke(
			handlePrimaryEndpoint,
			handleWebhookRoutes,
			// handleKafkaRoutes,
			buildMetricsRoutes,
			buildAPIAltRouter,
		),
	)
}

func handlePrimaryEndpoint(in primaryEndpointIn) {
	otelMuxOptions := []otelmux.Option{
		otelmux.WithTracerProvider(in.Tracing.TracerProvider()),
		otelmux.WithPropagators(in.Tracing.Propagator()),
	}

	in.Router.Use(
		otelmux.Middleware("mainSpan", otelMuxOptions...),
	)

	if in.V.IsSet(authAcquirerKey) {
		acquirer, err := createAuthAcquirer(in.AuthAcquirer)
		if err != nil {
			in.Logger.Error("Could not configure auth acquirer", zap.Error(err))
		} else {
			in.TranslationOptions.AuthAcquirer = acquirer
			in.StatServiceOptions.AuthAcquirer = acquirer
			in.Logger.Info("Outbound request authentication token acquirer enabled")
		}
	}
	ss := stat.NewService(in.StatServiceOptions)
	ts := translation.NewService(in.TranslationOptions)

	// Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes).
	stat.ConfigHandler(&stat.Options{
		S:                           ss,
		APIRouter:                   in.APIRouter,
		Authenticate:                &in.AuthChain,
		Log:                         in.Logger,
		ReducedLoggingResponseCodes: in.ReducedLoggingResponseCodes,
	})
	translation.ConfigHandler(&translation.Options{
		S:                           ts,
		APIRouter:                   in.APIRouter,
		Authenticate:                &in.AuthChain,
		Log:                         in.Logger,
		ValidServices:               in.TranslationServices,
		ReducedLoggingResponseCodes: in.ReducedLoggingResponseCodes,
	})
}

func handleWebhookRoutes(in handleWebhookRoutesIn) error {
	if in.PreviousVersion {
		if in.AddWebhookHandler != nil && in.GetAllWebhooksHandler != nil {
			fixV2Middleware, err := fixV2Duration(sallust.Get, in.WebhookConfig.Validation.TTL, in.V2AddWebhookHandler)
			if err != nil {
				fmt.Fprintf(os.Stderr, "Failed to initialize v2 endpoint middleware: %v\n", err)
				return err
			}
			in.APIRouter.Handle("/hook", in.AuthChain.Then(fixV2Middleware(candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator(), false)(in.AddWebhookHandler)))).Methods(http.MethodPost)
			in.APIRouter.Handle("/hooks", in.AuthChain.Then(candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator(), false)(in.GetAllWebhooksHandler)))
		}
	} else {
		in.APIRouter.Handle("/hook", in.AuthChain.Then(candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator(), false)(in.AddWebhookHandler))).Methods(http.MethodPost)
		in.APIRouter.Handle("/hooks", in.AuthChain.Then(candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator(), false)(in.GetAllWebhooksHandler)))
	}
	return nil
}

func metricMiddleware(f *touchstone.Factory) (out metricMiddlewareOut) {
	var bundle touchhttp.ServerBundle

	primary, err1 := bundle.NewInstrumenter(
		touchhttp.ServerLabel, "server_primary",
	)(f)
	alternate, err2 := bundle.NewInstrumenter(
		touchhttp.ServerLabel, "server_alternate",
	)(f)
	health, err3 := bundle.NewInstrumenter(
		touchhttp.ServerLabel, "server_health",
	)(f)

	if err1 != nil || err2 != nil || err3 != nil {
		return
	}

	out.Primary = alice.New(primary.Then)
	out.Alternate = alice.New(alternate.Then)
	out.Health = alice.New(health.Then)

	return
}

func provideAPIRouter(in apiRouterIn) *mux.Router {
	return in.PrimaryRouter.PathPrefix(in.URLPrefix).Subrouter()
}

func buildAPIAltRouter(in apiAltRouterIn) {
	apiAltRouter := in.AlternateRouter.PathPrefix(in.URLPrefix).Subrouter()
	apiAltRouter.Handle("/device/{deviceid}/{service}", in.APIRouter)
	apiAltRouter.Handle("/device/{deviceid}/{service}/{parameter}", in.APIRouter)
	apiAltRouter.Handle("/device/{deviceid}/stat", in.APIRouter)
	apiAltRouter.Handle("/hook", in.APIRouter)
	apiAltRouter.Handle("/hooks", in.APIRouter)
}

func provideURLPrefix(in provideURLPrefixIn) string {
	// if we want to support the previous API version, then include it in the
	// api base.
	urlPrefix := fmt.Sprintf("/%s", apiBase)
	if in.PrevVerSupport {
		urlPrefix = fmt.Sprintf("/%s", apiBaseDualVersion)
	}
	return urlPrefix
}

//nolint:funlen
func fixV2Duration(getLogger func(context.Context) *zap.Logger, config ancla.TTLVConfig, v2Handler http.Handler) (alice.Constructor, error) {
	if config.Now == nil {
		config.Now = time.Now
	}

	durationOpt := webhook.ValidateRegistrationDuration(config.Max)

	untilOpt := webhook.Until(config.Now, config.Jitter, config.Max)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			vars := mux.Vars(r)
			// if this isn't v2, do nothing.
			if vars == nil || vars["version"] != prevAPIVersion {
				next.ServeHTTP(w, r)
				return
			}

			// if this is v2, we need to unmarshal and check the duration.  If
			// the duration is bad, change it to 5m and add a header. Then use
			// the v2 handler.
			logger := sallusthttp.Get(r)

			requestPayload, err := ioutil.ReadAll(r.Body)
			if err != nil {
				v2ErrEncode(w, logger, err, 0)
				return
			}

			var wr webhook.RegistrationV1
			err = json.Unmarshal(requestPayload, &wr)
			if err != nil {
				var e *json.UnmarshalTypeError
				if errors.As(err, &e) {
					v2ErrEncode(w, logger,
						fmt.Errorf("%w: %v must be of type %v", errFailedWebhookUnmarshal, e.Field, e.Type),
						http.StatusBadRequest)
					return
				}
				v2ErrEncode(w, logger, fmt.Errorf("%w: %v", errFailedWebhookUnmarshal, err),
					http.StatusBadRequest)
				return
			}

			// check to see if the Webhook has a valid until/duration.
			// If not, set the WebhookRegistration  duration to 5m.
			if wr.Until.IsZero() {
				if wr.Duration == 0 {
					wr.Duration = webhook.CustomDuration(config.Max)
					w.Header().Add(v2WarningHeader,
						fmt.Sprintf("Unset duration and until fields will not be accepted in v3, webhook duration defaulted to %v", config.Max))
				} else {
					durationErr := durationOpt.Validate(&wr)
					if durationErr != nil {
						wr.Duration = webhook.CustomDuration(config.Max)
						w.Header().Add(v2WarningHeader,
							fmt.Sprintf("Invalid duration will not be accepted in v3: %v, webhook duration defaulted to %v", durationErr, config.Max))
					}
				}
			} else {
				untilErr := untilOpt.Validate(&wr)
				if untilErr != nil {
					wr.Until = time.Time{}
					wr.Duration = webhook.CustomDuration(config.Max)
					w.Header().Add(v2WarningHeader,
						fmt.Sprintf("Invalid until value will not be accepted in v3: %v, webhook duration defaulted to 5m", untilErr))
				}
			}

			// put the body back in the request
			body, err := json.Marshal(wr)
			if err != nil {
				v2ErrEncode(w, logger, fmt.Errorf("failed to recreate request body: %v", err), 0)
			}
			r.Body = ioutil.NopCloser(bytes.NewBuffer(body))

			if v2Handler == nil {
				v2Handler = next
			}
			v2Handler.ServeHTTP(w, r)
		})
	}, nil
}

func v2ErrEncode(w http.ResponseWriter, logger *zap.Logger, err error, code int) {
	if code == 0 {
		code = http.StatusInternalServerError
	}
	logger.Error("sending non-200, non-404 response",
		zap.Error(err), zap.Int("code", code))

	w.WriteHeader(code)

	json.NewEncoder(w).Encode(
		map[string]interface{}{
			"message": err.Error(),
		})
}

func buildMetricsRoutes(in metricsRoutesIn) {
	if in.Router != nil && in.Handler != nil {
		in.Router.Handle("/metrics", in.Handler).Methods("GET")
	}
}
