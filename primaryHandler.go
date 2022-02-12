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
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"syscall"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange/arrangehttp"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/acquire"
	bchecks "github.com/xmidt-org/bascule/basculechecks"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/key"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
	"github.com/xmidt-org/webpa-common/v2/basculechecks"
	"github.com/xmidt-org/webpa-common/v2/basculemetrics"
	"github.com/xmidt-org/webpa-common/v2/concurrent"
	"github.com/xmidt-org/webpa-common/v2/logging"
	"github.com/xmidt-org/webpa-common/v2/xhttp"
	"github.com/xmidt-org/webpa-common/v2/xmetrics"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.uber.org/fx"
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

func createAuthAcquirer(v *viper.Viper) (acquire.Acquirer, error) {
	var options authAcquirerConfig
	err := v.UnmarshalKey(authAcquirerKey, &options)

	if err != nil {
		return nil, err
	}

	if options.JWT.AuthURL != "" && options.JWT.Buffer != 0 && options.JWT.Timeout != 0 {
		return acquire.NewRemoteBearerTokenAcquirer(options.JWT)
	}

	if options.Basic != "" {
		return acquire.NewFixedAuthAcquirer(options.Basic)
	}

	return nil, errors.New("auth acquirer not configured properly")
}

// authenticationHandler configures the authorization requirements for requests to reach the main handler
//nolint:funlen
func authenticationHandler(v *viper.Viper, logger log.Logger, registry xmetrics.Registry) (*alice.Chain, error) {
	if registry == nil {
		return nil, errors.New("nil registry")
	}

	basculeMeasures := basculemetrics.NewAuthValidationMeasures(registry)
	capabilityCheckMeasures := basculechecks.NewAuthCapabilityCheckMeasures(registry)
	listener := basculemetrics.NewMetricListener(basculeMeasures)

	basicAllowed := make(map[string]string)
	basicAuth := v.GetStringSlice("authHeader")
	for _, a := range basicAuth {
		decoded, err := base64.StdEncoding.DecodeString(a)
		if err != nil {
			logging.Info(logger).Log(logging.MessageKey(), "failed to decode auth header", "authHeader", a, logging.ErrorKey(), err.Error())
			continue
		}

		i := bytes.IndexByte(decoded, ':')
		logging.Debug(logger).Log(logging.MessageKey(), "decoded string", "string", decoded, "i", i)
		if i > 0 {
			basicAllowed[string(decoded[:i])] = string(decoded[i+1:])
		}
	}
	logging.Debug(logger).Log(logging.MessageKey(), "Created list of allowed basic auths", "allowed", basicAllowed, "config", basicAuth)

	options := []basculehttp.COption{
		basculehttp.WithCLogger(getLogger),
		basculehttp.WithCErrorResponseFunc(listener.OnErrorResponse),
		basculehttp.WithParseURLFunc(basculehttp.CreateRemovePrefixURLFunc("/"+apiBase+"/", basculehttp.DefaultParseURLFunc)),
	}
	if len(basicAllowed) > 0 {
		options = append(options, basculehttp.WithTokenFactory("Basic", basculehttp.BasicTokenFactory(basicAllowed)))
	}
	var jwtVal JWTValidator

	v.UnmarshalKey("jwtValidator", &jwtVal)
	if jwtVal.Keys.URI != "" {
		resolver, err := jwtVal.Keys.NewResolver()
		if err != nil {
			return &alice.Chain{}, emperror.With(err, "failed to create resolver")
		}

		options = append(options, basculehttp.WithTokenFactory("Bearer", basculehttp.BearerTokenFactory{
			DefaultKeyID: DefaultKeyID,
			Resolver:     resolver,
			Parser:       bascule.DefaultJWTParser,
			Leeway:       jwtVal.Leeway,
		}))
	}

	authConstructor := basculehttp.NewConstructor(options...)

	bearerRules := bascule.Validators{
		bchecks.NonEmptyPrincipal(),
		bchecks.NonEmptyType(),
		bchecks.ValidType([]string{"jwt"}),
	}

	// only add capability check if the configuration is set
	var capabilityCheck CapabilityConfig
	v.UnmarshalKey("capabilityCheck", &capabilityCheck)
	if capabilityCheck.Type == "enforce" || capabilityCheck.Type == "monitor" {
		var endpoints []*regexp.Regexp
		c, err := basculechecks.NewEndpointRegexCheck(capabilityCheck.Prefix, capabilityCheck.AcceptAllMethod)
		if err != nil {
			return nil, emperror.With(err, "failed to create capability check")
		}
		for _, e := range capabilityCheck.EndpointBuckets {
			r, err := regexp.Compile(e)
			if err != nil {
				logging.Error(logger).Log(logging.MessageKey(), "failed to compile regular expression", "regex", e, logging.ErrorKey(), err.Error())
				continue
			}
			endpoints = append(endpoints, r)
		}
		m := basculechecks.MetricValidator{
			C:         basculechecks.CapabilitiesValidator{Checker: c},
			Measures:  capabilityCheckMeasures,
			Endpoints: endpoints,
		}
		bearerRules = append(bearerRules, m.CreateValidator(capabilityCheck.Type == "enforce"))
	}

	authEnforcer := basculehttp.NewEnforcer(
		basculehttp.WithELogger(getLogger),
		basculehttp.WithRules("Basic", bascule.Validators{
			bchecks.AllowAll(),
		}),
		basculehttp.WithRules("Bearer", bearerRules),
		basculehttp.WithEErrorResponseFunc(listener.OnErrorResponse),
	)
	constructors := []alice.Constructor{setLogger(logger), authConstructor, authEnforcer, basculehttp.NewListenerDecorator(listener)}

	chain := alice.New(constructors...)
	return &chain, nil
}

type webhookHandlerConfigIn struct {
	fx.In
	v                   viper.Viper
	webhookConfigKey    ancla.Config
	argusClientConfigIn ArgusClientTimeoutConfigIn
	logger              log.Logger
	metricsRegistry     xmetrics.Registry
	tracing             candlelight.Tracing
	APIRouter           *mux.Router
	authenticate        *alice.Chain
}

func webhookHandler(in webhookHandlerConfigIn) error {
	//
	// Webhooks (if not configured, handlers are not set up)
	//
	if in.v.IsSet(webhookConfigKey) {
		webhookConfig := in.webhookConfigKey

		webhookConfig.Logger = in.logger
		webhookConfig.MetricsProvider = in.metricsRegistry
		argusClientTimeout := newArgusClientTimeout(in.argusClientConfigIn)

		webhookConfig.Argus.HTTPClient = newHTTPClient(argusClientTimeout, in.tracing)

		svc, stopWatch, err := ancla.Initialize(webhookConfig, getLogger, logging.WithLogger)
		if err != nil {
			return fmt.Errorf("failed to initialize webhook service: %s", err)
		}
		defer stopWatch()

		builtValidators, err := ancla.BuildValidators(webhookConfig.Validation)
		if err != nil {
			return fmt.Errorf("failed to initialize webhook validators: %s", err)
		}

		addWebhookHandler := ancla.NewAddWebhookHandler(svc, ancla.HandlerConfig{
			MetricsProvider:   in.metricsRegistry,
			V:                 builtValidators,
			DisablePartnerIDs: webhookConfig.DisablePartnerIDs,
			GetLogger:         getLogger,
		})

		getAllWebhooksHandler := ancla.NewGetAllWebhooksHandler(svc, ancla.HandlerConfig{
			GetLogger: getLogger,
		})

		in.APIRouter.Handle("/hook", in.authenticate.Then(addWebhookHandler)).Methods(http.MethodPost)
		in.APIRouter.Handle("/hooks", in.authenticate.Then(getAllWebhooksHandler)).Methods(http.MethodGet)
		level.Info(in.logger).Log("Webhook service enabled")
	} else {
		level.Info(in.logger).Log(logging.MessageKey(), "Webhook service disabled")
	}
	return nil
}

func ProvideHandlers() fx.Option {
	return fx.Provide(
		webhookHandler,
		authenticationHandler,
	)
}

func statServiceProvider(in ServiceConfigIn) *stat.ServiceOptions {
	//
	// Stat Service configs
	//
	return &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   in.logger,
						Retries:  in.v.GetInt(reqMaxRetriesKey),
						Interval: in.v.GetDuration(reqRetryIntervalKey),
					},
					in.xmidtHTTPClient.Do),
				RequestTimeout: in.xmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", in.v.GetString(targetURLKey)),
	}
}

type ServiceConfigIn struct {
	fx.In
	v                  viper.Viper
	logger             log.Logger
	xmidtHTTPClient    *http.Client
	xmidtClientTimeout httpClientTimeout
}

func translationOptionsProvider(in ServiceConfigIn) *translation.ServiceOptions {
	//
	// WRP Service configs
	//
	return &translation.ServiceOptions{
		XmidtWrpURL: fmt.Sprintf("%s/device", in.v.GetString(targetURLKey)),
		WRPSource:   in.v.GetString(wrpSourceKey),
		T: transaction.New(
			&transaction.Options{
				RequestTimeout: in.xmidtClientTimeout.RequestTimeout,
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   in.logger,
						Retries:  in.v.GetInt(reqMaxRetriesKey),
						Interval: in.v.GetDuration(reqRetryIntervalKey),
					},
					in.xmidtHTTPClient.Do),
			}),
	}
}

type authAcquirerConfigIn struct {
	v                           *viper.Viper
	logger                      log.Logger
	statServiceOptions          *stat.ServiceOptions
	translationOptions          *translation.ServiceOptions
	APIRouter                   *mux.Router
	authenticate                *alice.Chain
	reducedLoggingResponseCodes []int
}

func authAcquirerHandler(in authAcquirerConfigIn) {
	reducedLoggingResponseCodes := in.v.GetIntSlice(reducedTransactionLoggingCodesKey)

	if in.v.IsSet(authAcquirerKey) {
		acquirer, err := createAuthAcquirer(in.v)
		if err != nil {
			level.Error(in.logger).Log(logging.MessageKey(), "Could not configure auth acquirer", logging.ErrorKey(), err)
		} else {
			in.translationOptions.AuthAcquirer = acquirer
			in.statServiceOptions.AuthAcquirer = acquirer
			level.Info(in.logger).Log(logging.MessageKey(), "Outbound request authentication token acquirer enabled")
		}
	}
	ss := stat.NewService(in.statServiceOptions)
	ts := translation.NewService(in.translationOptions)
	// Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes).
	stat.ConfigHandler(&stat.Options{
		S:                           ss,
		APIRouter:                   in.APIRouter,
		Authenticate:                in.authenticate,
		Log:                         in.logger,
		ReducedLoggingResponseCodes: reducedLoggingResponseCodes,
	})

	translation.ConfigHandler(&translation.Options{
		S:                           ts,
		APIRouter:                   in.APIRouter,
		Authenticate:                in.authenticate,
		Log:                         in.logger,
		ValidServices:               in.v.GetStringSlice(translationServicesKey),
		ReducedLoggingResponseCodes: reducedLoggingResponseCodes,
	})
}

func provideServers() fx.Option {
	return fx.Options(
		arrangehttp.Server{
			Name: "server_primary",
			Key:  "primary",
		}.Provide(),
		arrangehttp.Server{
			Name: "server_health",
			Key:  "health",
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

type PrimaryRouterIn struct {
	fx.In
	Router    *mux.Router `name:"server_primary"`
	APIBase   string      `name:"api_base"`
	AuthChain alice.Chain `name:"auth_chain"`
	Tracing   candlelight.Tracing
	Logger    log.Logger
}

func handlePrimaryEndpoint(in PrimaryRouterIn) {
	level.Info(in.Logger).Log(logging.MessageKey(), "tracing status", "enabled", !in.Tracing.IsNoop())
	otelMuxOptions := []otelmux.Option{
		otelmux.WithPropagators(in.Tracing.Propagator()),
		otelmux.WithTracerProvider(in.Tracing.TracerProvider()),
	}

	in.Router.Use(otelmux.Middleware("mainSpan", otelMuxOptions...),
		candlelight.EchoFirstTraceNodeInfo(in.Tracing.Propagator()),
	)
}

func runServers(logger log.Logger) error {
	var (
		_, tr1d1umServer, done = webPA.Prepare(logger, nil, metricsRegistry, rootRouter)
		signals                = make(chan os.Signal, 10)
	)

	//
	// Execute the runnable, which runs all the servers, and wait for a signal
	//
	waitGroup, shutdown, err := concurrent.Execute(tr1d1umServer)

	if err != nil {
		level.Error(logger).Log(logging.MessageKey(), "Unable to start tr1d1um", logging.ErrorKey(), err)
		return err
	}

	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	for exit := false; !exit; {
		select {
		case s := <-signals:
			level.Error(logger).Log(logging.MessageKey(), "exiting due to signal", "signal", s)
			exit = true
		case <-done:
			level.Error(logger).Log(logging.MessageKey(), "one or more servers exited")
			exit = true
		}
	}

	close(shutdown)
	waitGroup.Wait()
	return nil
}

func provideAPIRouter() *mux.Router {
	rootRouter := mux.NewRouter()
	APIRouter := rootRouter.PathPrefix(fmt.Sprintf("/%s/", apiBase)).Subrouter()
	return APIRouter
}
