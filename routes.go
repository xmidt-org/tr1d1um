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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/arrange/arrangehttp"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/translation"
	"github.com/xmidt-org/webpa-common/logging"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var (
	errFailedWebhookUnmarshal = errors.New("failed to JSON unmarshal webhook")

	v2WarningHeader = "X-Xmidt-Warning"
)

type PrimaryEndpointIn struct {
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

type HandleWebhookRoutesIn struct {
	fx.In
	Logger                *zap.Logger
	APIRouter             *mux.Router  `name:"api_router"`
	AuthChain             alice.Chain  `name:"auth_chain"`
	AddWebhookHandler     http.Handler `name:"add_webhook_handler"`
	V2AddWebhookHandler   http.Handler `name:"v2_add_webhook_handler"`
	GetAllWebhooksHandler http.Handler `name:"get_all_webhooks_handler"`
	WebhookConfigKey      ancla.Config
}

type APIRouterIn struct {
	fx.In
	PrimaryRouter *mux.Router `name:"server_primary"`
	URLPrefix     string      `name:"url_prefix"`
}

type ProvideURLPrefixIn struct {
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
				PrimaryMMIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_health",
			Key:  "servers.health",
			Inject: arrange.Inject{
				HealthMMIn{},
			},
		}.Provide(),
		arrangehttp.Server{
			Name: "server_pprof",
			Key:  "servers.pprof",
		}.Provide(),
		arrangehttp.Server{
			Name: "server_metrics",
			Key:  "servers.metric",
		}.Provide(),
		fx.Invoke(
			handlePrimaryEndpoint,
			handleWebhookRoutes,
		),
	)
}

func handlePrimaryEndpoint(in PrimaryEndpointIn) {
	otelMuxOptions := []otelmux.Option{
		otelmux.WithTracerProvider(in.Tracing.TracerProvider()),
		otelmux.WithPropagators(in.Tracing.Propagator()),
	}

	in.Router.Use(
		otelmux.Middleware("mainSpan", otelMuxOptions...),
		candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator()),
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

func handleWebhookRoutes(in HandleWebhookRoutesIn) error {
	if in.AddWebhookHandler != nil && in.GetAllWebhooksHandler != nil {
		fixV2Middleware, err := fixV2Duration(getLogger, in.WebhookConfigKey.Validation.TTL, in.V2AddWebhookHandler)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to initialize v2 endpoint middleware: %v\n", err)
			return err
		}
		in.APIRouter.Handle("/hook", in.AuthChain.Then(fixV2Middleware(in.GetAllWebhooksHandler))).Methods(http.MethodPost)
		in.APIRouter.Handle("/hooks", in.AuthChain.Then(in.GetAllWebhooksHandler)).Methods(http.MethodGet)
	}
	return nil
}

func metricMiddleware(bundle touchhttp.ServerBundle) (out MetricMiddlewareOut) {
	out.Primary = alice.New(bundle.ForServer("server_primary").Then)
	out.Health = alice.New(bundle.ForServer("server_health").Then)
	return
}

func provideAPIRouter(in APIRouterIn) *mux.Router {
	APIRouter := in.PrimaryRouter.PathPrefix(in.URLPrefix).Subrouter()
	return APIRouter
}

func provideURLPrefix(in ProvideURLPrefixIn) string {
	// if we want to support the previous API version, then include it in the
	// api base.
	urlPrefix := fmt.Sprintf("/%s", apiBase)
	if in.PrevVerSupport {
		urlPrefix = fmt.Sprintf("/%s", apiBaseDualVersion)
	}
	return urlPrefix
}

//nolint:funlen
func fixV2Duration(getLogger ancla.GetLoggerFunc, config ancla.TTLVConfig, v2Handler http.Handler) (alice.Constructor, error) {
	if getLogger == nil {
		getLogger = func(_ context.Context) log.Logger {
			return nil
		}
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	durationCheck, err := ancla.CheckDuration(config.Max)
	if err != nil {
		return nil, fmt.Errorf("failed to create duration check: %v", err)
	}
	untilCheck, err := ancla.CheckUntil(config.Jitter, config.Max, config.Now)
	if err != nil {
		return nil, fmt.Errorf("failed to create until check: %v", err)
	}

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
			logger := getLogger(r.Context())
			if logger == nil {
				logger = log.NewNopLogger()
			}
			requestPayload, err := ioutil.ReadAll(r.Body)
			if err != nil {
				v2ErrEncode(w, logger, err, 0)
				return
			}

			var wr ancla.WebhookRegistration
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
			webhook := wr.ToWebhook()
			if webhook.Until.IsZero() {
				if webhook.Duration == 0 {
					wr.Duration = ancla.CustomDuration(config.Max)
					w.Header().Add(v2WarningHeader,
						fmt.Sprintf("Unset duration and until fields will not be accepted in v3, webhook duration defaulted to %v", config.Max))
				} else {
					durationErr := durationCheck(webhook)
					if durationErr != nil {
						wr.Duration = ancla.CustomDuration(config.Max)
						w.Header().Add(v2WarningHeader,
							fmt.Sprintf("Invalid duration will not be accepted in v3: %v, webhook duration defaulted to %v", durationErr, config.Max))
					}
				}
			} else {
				untilErr := untilCheck(webhook)
				if untilErr != nil {
					wr.Until = time.Time{}
					wr.Duration = ancla.CustomDuration(config.Max)
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

func v2ErrEncode(w http.ResponseWriter, logger log.Logger, err error, code int) {
	if code == 0 {
		code = http.StatusInternalServerError
	}
	level.Error(logger).Log(logging.MessageKey(), "sending non-200, non-404 response",
		logging.ErrorKey(), err, "code", code)

	w.WriteHeader(code)

	json.NewEncoder(w).Encode(
		map[string]interface{}{
			"message": err.Error(),
		})
}
