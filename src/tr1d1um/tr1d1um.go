/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
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
	_ "net/http/pprof"
	"os"
	"os/signal"
	"time"

	"tr1d1um/common"
	"tr1d1um/hooks"
	"tr1d1um/stat"
	"tr1d1um/translation"

	"github.com/Comcast/webpa-common/basculechecks"
	"github.com/Comcast/webpa-common/client"

	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/webhook"
	"github.com/Comcast/webpa-common/webhook/aws"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

const (
	DefaultKeyID             = "current"
	applicationName, apiBase = "tr1d1um", "api/v2"

	translationServicesKey = "supportedServices"
	targetURLKey           = "targetURL"
	netDialerTimeoutKey    = "netDialerTimeout"
	clientTimeoutKey       = "clientTimeout"
	reqTimeoutKey          = "respWaitTimeout"
	reqRetryIntervalKey    = "requestRetryInterval"
	reqMaxRetriesKey       = "requestMaxRetries"
	WRPSourcekey           = "WRPSource"
	hooksSchemeKey         = "hooksScheme"
	applicationVersion     = "0.1.2"
)

var defaults = map[string]interface{}{
	translationServicesKey: []string{}, // no services allowed by the default
	targetURLKey:           "localhost:6000",
	// netDialerTimeoutKey:    "5s",
	// clientTimeoutKey:       "50s",
	reqTimeoutKey: "40s",
	// reqRetryIntervalKey: "2s",
	reqMaxRetriesKey: 2,
	WRPSourcekey:     "dns:localhost",
	hooksSchemeKey:   "https",
}

func tr1d1um(arguments []string) (exitCode int) {

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, webPA, err = server.Initialize(applicationName, arguments, f, v, webhook.Metrics, aws.Metrics, basculechecks.Metrics)
	)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize viper: %s\n", err.Error())
		return 1
	}

	oc := client.ClientMetricOptions{InFlight: true, RequestDuration: true, RequestCounter: true, DroppedMessages: true, OutboundRetries: true}
	webPAClient, err := client.Initialize(v, metricsRegistry, logger, oc, nil, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize client: %s\n", err.Error())
		return 1
	}

	var (
		infoLogger, errorLogger = logging.Info(logger), logging.Error(logger)
		authenticate            *alice.Chain
	)

	// This allows us to communicate the version of the binary upon request.
	printVer := f.BoolP("version", "v", false, "displays the version number")

	if *printVer {
		fmt.Println(applicationVersion)
		return 0
	}

	for k, va := range defaults {
		v.SetDefault(k, va)
	}

	infoLogger.Log("configurationFile", v.ConfigFileUsed())
	reqTimeout, err := time.ParseDuration(v.GetString("reqTimeoutKey"))
	if err != nil {
		errorLogger.Log(logging.MessageKey(), "Need reqTimeOutKey", logging.ErrorKey(), err)
		return 4
	}

	r := mux.NewRouter()
	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

	APIRouter := r.PathPrefix(fmt.Sprintf("/%s/", apiBase)).Subrouter()

	authenticateHandler, err := NewAuthenticationHandler(v, logger, metricsRegistry)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to build authentication handler: %s\n", err.Error())
		return 1
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to parse timeout configuration values: %s \n", err.Error())
		return 1
	}

	//
	// Webhooks (if not configured, handler for webhooks is not set up)
	//
	var snsFactory *webhook.Factory

	if accessKey := v.GetString("aws.accessKey"); accessKey != "" && accessKey != "fake-accessKey" { //only proceed if sure that value was set and not the default one
		snsFactory, err = webhook.NewFactory(v)

		if err != nil {
			fmt.Fprintf(os.Stderr, "Error creating new webHook factory: %s\n", err.Error())
			return 1
		}
	}

	if snsFactory != nil {
		hooks.ConfigHandler(&hooks.Options{
			APIRouter:    APIRouter,
			RootRouter:   r,
			SoAProvider:  v.GetString("soa.provider"),
			Authenticate: authenticateHandler,
			M:            metricsRegistry,
			Host:         v.GetString("fqdn") + v.GetString("primary.address"),
			HooksFactory: snsFactory,
			Log:          logger,
			Scheme:       v.GetString(hooksSchemeKey),
		})
	}

	//
	// Stat Service
	//
	ss := stat.NewService(&stat.ServiceOptions{
		Tr1d1umTransactor: common.NewTr1d1umTransactor(webPAClient, reqTimeout),
		XmidtStatURL:      fmt.Sprintf("%s/%s/device/${device}/stat", v.GetString(targetURLKey), apiBase),
	})

	//Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes)
	stat.ConfigHandler(&stat.Options{
		S:            ss,
		APIRouter:    APIRouter,
		Authenticate: authenticateHandler,
		Log:          logger,
	})

	//
	// WRP Service
	//

	ts := translation.NewService(&translation.ServiceOptions{
		XmidtWrpURL:       fmt.Sprintf("%s/%s/device", v.GetString(targetURLKey), apiBase),
		WRPSource:         v.GetString(WRPSourcekey),
		Tr1d1umTransactor: common.NewTr1d1umTransactor(webPAClient, reqTimeout),
	})

	translation.ConfigHandler(&translation.Options{
		S:             ts,
		APIRouter:     APIRouter,
		Authenticate:  authenticateHandler,
		Log:           logger,
		ValidServices: v.GetStringSlice(translationServicesKey),
	})

	var (
		_, tr1d1umServer, _ = webPA.Prepare(logger, nil, metricsRegistry, r)
		signals             = make(chan os.Signal, 1)
	)

	//
	// Execute the runnable, which runs all the servers, and wait for a signal
	//
	waitGroup, shutdown, err := concurrent.Execute(tr1d1umServer)

	if err != nil {
		errorLogger.Log(logging.MessageKey(), "Unable to start tr1d1um", logging.ErrorKey(), err)
		return 4
	}

	if snsFactory != nil {
		// wait for DNS to propagate before subscribing to SNS
		if err = snsFactory.DnsReady(); err == nil {
			infoLogger.Log(logging.MessageKey(), "server is ready to take on subscription confirmations")
			snsFactory.PrepareAndStart()
		} else {
			errorLogger.Log(logging.MessageKey(), "Server was not ready within a time constraint. SNS confirmation could not happen",
				logging.ErrorKey(), err)
		}
	}

	signal.Notify(signals)
	s := server.SignalWait(infoLogger, signals, os.Kill, os.Interrupt)
	errorLogger.Log(logging.MessageKey(), "exiting due to signal", "signal", s)
	close(shutdown)
	waitGroup.Wait()

	return 0
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
