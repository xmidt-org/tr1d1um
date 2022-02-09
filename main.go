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
	"io"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/sallust/sallustkit"
	"github.com/xmidt-org/tr1d1um/stat"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/tr1d1um/translation"
	"go.opentelemetry.io/contrib/instrumentation/github.com/gorilla/mux/otelmux"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/webpa-common/v2/concurrent"
	"github.com/xmidt-org/webpa-common/v2/logging"
	"github.com/xmidt-org/webpa-common/v2/xhttp"
)

// convenient global values
const (
	DefaultKeyID             = "current"
	applicationName, apiBase = "tr1d1um", "api/v3"
)

const (
	translationServicesKey            = "supportedServices"
	targetURLKey                      = "targetURL"
	netDialerTimeoutKey               = "netDialerTimeout"
	clientTimeoutKey                  = "clientTimeout"
	reqTimeoutKey                     = "respWaitTimeout"
	reqRetryIntervalKey               = "requestRetryInterval"
	reqMaxRetriesKey                  = "requestMaxRetries"
	wrpSourceKey                      = "WRPSource"
	hooksSchemeKey                    = "hooksScheme"
	reducedTransactionLoggingCodesKey = "log.reducedLoggingResponseCodes"
	authAcquirerKey                   = "authAcquirer"
	webhookConfigKey                  = "webhook"
	tracingConfigKey                  = "tracing"
)

var (
	// dynamic versioning
	Version   string
	BuildTime string
	GitCommit string
)

var defaults = map[string]interface{}{
	translationServicesKey: []string{}, // no services allowed by the default
	targetURLKey:           "localhost:6000",
	netDialerTimeoutKey:    "5s",
	clientTimeoutKey:       "50s",
	reqTimeoutKey:          "40s",
	reqRetryIntervalKey:    "2s",
	reqMaxRetriesKey:       2,
	wrpSourceKey:           "dns:localhost",
	hooksSchemeKey:         "https",
}

//nolint:funlen
func tr1d1um(arguments []string) (exitCode int) {

	v, l, f, err := setup(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logger := gokitLogger(l)
	// _, metricsRegistry, webPA, err := server.Initialize(applicationName, arguments, f, v, ancla.Metrics, basculechecks.Metrics, basculemetrics.Metrics)

	// This allows us to communicate the version of the binary upon request.
	if parseErr, done := printVersion(f, arguments); done {
		// if we're done, we're exiting no matter what
		exitIfError(logger, emperror.Wrap(parseErr, "failed to parse arguments"))
		os.Exit(0)
	}

	app := fx.New(
		arrange.ForViper(v),
		arrange.LoggerFunc(l.Sugar().Infof),
		fx.Supply(logger),
		fx.Provide(
			gokitLogger,
			arrange.UnmarshalKey("xmidtClientTimeout", httpClientTimeout{}),
			arrange.UnmarshalKey("argusClientTimeout", httpClientTimeout{}),
			arrange.UnmarshalKey("tracingConfigKey", candlelight.Config{}),
			arrange.UnmarshalKey("authAcquirerKey", authAcquirerConfig{}),
			arrange.UnmarshalKey("webhookConfigKey", ancla.Config{}),
			provideServers,
		),
	)

	switch err := app.Err(); {
	case errors.Is(err, pflag.ErrHelp):
		return
	case err == nil:
		app.Run()
	default:
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	var (
		infoLogger, errorLogger = logging.Info(logger), logging.Error(logger)
		authenticate            *alice.Chain
	)

	for k, va := range defaults {
		v.SetDefault(k, va)
	}

	infoLogger.Log("configurationFile", v.ConfigFileUsed())

	tracing, err := loadTracing(v, applicationName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to build tracing component: %v \n", err)
		return 1
	}
	infoLogger.Log(logging.MessageKey(), "tracing status", "enabled", !tracing.IsNoop())
	authenticate, err = authenticationHandler(v, logger, metricsRegistry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to build authentication handler: %s\n", err.Error())
		return 1
	}

	rootRouter := mux.NewRouter()
	otelMuxOptions := []otelmux.Option{
		otelmux.WithPropagators(tracing.Propagator()),
		otelmux.WithTracerProvider(tracing.TracerProvider()),
	}
	rootRouter.Use(otelmux.Middleware("mainSpan", otelMuxOptions...), candlelight.EchoFirstTraceNodeInfo(tracing.Propagator()))

	APIRouter := rootRouter.PathPrefix(fmt.Sprintf("/%s/", apiBase)).Subrouter()

	//
	// Webhooks (if not configured, handlers are not set up)
	//
	if v.IsSet(webhookConfigKey) {
		err := webhookHandler(v, logger, metricsRegistry, tracing, APIRouter, authenticate, infoLogger)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	} else {
		infoLogger.Log(logging.MessageKey(), "Webhook service disabled")
	}

	xmidtClientTimeout := newXmidtClientTimeout(v)
	// if err != nil {
	// 	fmt.Fprintf(os.Stderr, "Unable to parse xmidt client timeout config values: %s \n", err.Error())
	// 	return 1
	// }
	xmidtHTTPClient := newHTTPClient(xmidtClientTimeout, tracing)

	//
	// Stat Service configs
	//
	statServiceOptions := &stat.ServiceOptions{
		HTTPTransactor: transaction.New(
			&transaction.Options{
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   logger,
						Retries:  v.GetInt(reqMaxRetriesKey),
						Interval: v.GetDuration(reqRetryIntervalKey),
					},
					xmidtHTTPClient.Do),
				RequestTimeout: xmidtClientTimeout.RequestTimeout,
			}),
		XmidtStatURL: fmt.Sprintf("%s/device/${device}/stat", v.GetString(targetURLKey)),
	}

	//
	// WRP Service configs
	//
	translationOptions := &translation.ServiceOptions{
		XmidtWrpURL: fmt.Sprintf("%s/device", v.GetString(targetURLKey)),
		WRPSource:   v.GetString(wrpSourceKey),
		T: transaction.New(
			&transaction.Options{
				RequestTimeout: xmidtClientTimeout.RequestTimeout,
				Do: xhttp.RetryTransactor( //nolint:bodyclose
					xhttp.RetryOptions{
						Logger:   logger,
						Retries:  v.GetInt(reqMaxRetriesKey),
						Interval: v.GetDuration(reqRetryIntervalKey),
					},
					xmidtHTTPClient.Do),
			}),
	}

	reducedLoggingResponseCodes := v.GetIntSlice(reducedTransactionLoggingCodesKey)

	if v.IsSet(authAcquirerKey) {
		acquirer, err := createAuthAcquirer(v)
		if err != nil {
			errorLogger.Log(logging.MessageKey(), "Could not configure auth acquirer", logging.ErrorKey(), err)
		} else {
			translationOptions.AuthAcquirer = acquirer
			statServiceOptions.AuthAcquirer = acquirer
			infoLogger.Log(logging.MessageKey(), "Outbound request authentication token acquirer enabled")
		}
	}

	ss := stat.NewService(statServiceOptions)
	ts := translation.NewService(translationOptions)

	// Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes).
	stat.ConfigHandler(&stat.Options{
		S:                           ss,
		APIRouter:                   APIRouter,
		Authenticate:                authenticate,
		Log:                         logger,
		ReducedLoggingResponseCodes: reducedLoggingResponseCodes,
	})

	translation.ConfigHandler(&translation.Options{
		S:                           ts,
		APIRouter:                   APIRouter,
		Authenticate:                authenticate,
		Log:                         logger,
		ValidServices:               v.GetStringSlice(translationServicesKey),
		ReducedLoggingResponseCodes: reducedLoggingResponseCodes,
	})

	var (
		_, tr1d1umServer, done = webPA.Prepare(logger, nil, metricsRegistry, rootRouter)
		signals                = make(chan os.Signal, 10)
	)

	//
	// Execute the runnable, which runs all the servers, and wait for a signal
	//
	waitGroup, shutdown, err := concurrent.Execute(tr1d1umServer)

	if err != nil {
		errorLogger.Log(logging.MessageKey(), "Unable to start tr1d1um", logging.ErrorKey(), err)
		return 4
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

	return 0
}

type xmidtClientTimeoutConfigIn struct {
	fx.In
	xmidtClientTimeout httpClientTimeout
}

func newXmidtClientTimeout(in xmidtClientTimeoutConfigIn) httpClientTimeout {
	xct := in.xmidtClientTimeout

	if xct.ClientTimeout == 0 {
		xct.ClientTimeout = time.Second * 135
	}
	if xct.NetDialerTimeout == 0 {
		xct.NetDialerTimeout = time.Second * 5
	}
	if xct.RequestTimeout == 0 {
		xct.RequestTimeout = time.Second * 129
	}
	return xct
}

type argusClientTimeoutConfigIn struct {
	fx.In
	argusClientTimeout httpClientTimeout
}

func newArgusClientTimeout(in argusClientTimeoutConfigIn) httpClientTimeout {
	act := in.argusClientTimeout

	if act.ClientTimeout == 0 {
		act.ClientTimeout = time.Second * 50
	}
	if act.NetDialerTimeout == 0 {
		act.NetDialerTimeout = time.Second * 5
	}
	return act
}

type loadTracingConfigIn struct {
	fx.In
	loadTracingConfig candlelight.Config
	appName           string
}

func loadTracing(in loadTracingConfigIn) (candlelight.Tracing, error) {
	traceConfig := in.loadTracingConfig
	traceConfig.ApplicationName = in.appName
	tracing, err := candlelight.New(traceConfig)
	if err != nil {
		return candlelight.Tracing{}, err
	}
	return tracing, nil
}

func printVersion(f *pflag.FlagSet, arguments []string) (error, bool) {
	printVer := f.BoolP("version", "v", false, "displays the version number")
	if err := f.Parse(arguments); err != nil {
		return err, true
	}

	if *printVer {
		printVersionInfo(os.Stdout)
		return nil, true
	}
	return nil, false
}

func exitIfError(logger log.Logger, err error) {
	if err != nil {
		if logger != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.ErrorKey(), err.Error())
		}
		fmt.Fprintf(os.Stderr, "Error: %#v\n", err.Error())
		os.Exit(1)
	}
}

func printVersionInfo(writer io.Writer) {
	fmt.Fprintf(writer, "%s:\n", applicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", BuildTime)
	fmt.Fprintf(writer, "  git commit: \t%s\n", GitCommit)
	fmt.Fprintf(writer, "  os/arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func gokitLogger(l *zap.Logger) log.Logger {
	return sallustkit.Logger{
		Zap: l,
	}
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
