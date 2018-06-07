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
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"time"

	"github.com/Comcast/webpa-common/xhttp"

	"tr1d1um/hooks"
	"tr1d1um/stat"
	"tr1d1um/translation"

	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/handler"
	"github.com/Comcast/webpa-common/secure/key"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/webhook"
	"github.com/Comcast/webpa-common/webhook/aws"
	"github.com/SermoDigital/jose/jwt"

	"github.com/Comcast/webpa-common/xmetrics"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

//convenient global values
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
)

var defaults = map[string]interface{}{
	translationServicesKey: []string{}, // no services allowed by the default
	targetURLKey:           "localhost:6000",
	netDialerTimeoutKey:    "5s",
	clientTimeoutKey:       "50s",
	reqTimeoutKey:          "40s",
	reqRetryIntervalKey:    "2s",
	reqMaxRetriesKey:       2,
	WRPSourcekey:           "dns:localhost",
	hooksSchemeKey:         "https",
}

func tr1d1um(arguments []string) (exitCode int) {

	var (
		f, v                                = pflag.NewFlagSet(applicationName, pflag.ContinueOnError), viper.New()
		logger, metricsRegistry, webPA, err = server.Initialize(applicationName, arguments, f, v, webhook.Metrics, aws.Metrics, secure.Metrics)
	)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize viper: %s\n", err.Error())
		return 1
	}

	var (
		infoLogger, errorLogger = logging.Info(logger), logging.Error(logger)
		authenticate            *alice.Chain
	)

	for k, va := range defaults {
		v.SetDefault(k, va)
	}

	infoLogger.Log("configurationFile", v.ConfigFileUsed())

	r := mux.NewRouter()

	APIRouter := r.PathPrefix(fmt.Sprintf("/%s/", apiBase)).Subrouter()

	authenticate, err = authenticationHandler(v, logger, metricsRegistry)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to build authentication handler: %s\n", err.Error())
		return 1
	}

	tConfigs, err := newTimeoutConfigs(v)

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
			Authenticate: authenticate,
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
		Do: xhttp.RetryTransactor(xhttp.RetryOptions{
			Logger:   logger,
			Retries:  v.GetInt(reqMaxRetriesKey),
			Interval: v.GetDuration(reqRetryIntervalKey),
		}, newClient(v, tConfigs).Do),

		CtxTimeout: tConfigs.rTimeout,

		XmidtStatURL: fmt.Sprintf("%s/%s/device/${device}/stat", v.GetString(targetURLKey), apiBase),
	})

	//Must be called before translation.ConfigHandler due to mux path specificity (https://github.com/gorilla/mux#matching-routes)
	stat.ConfigHandler(&stat.Options{
		S:            ss,
		APIRouter:    APIRouter,
		Authenticate: authenticate,
		Log:          logger,
	})

	//
	// WRP Service
	//

	ts := translation.NewService(&translation.ServiceOptions{
		XmidtWrpURL: fmt.Sprintf("%s/%s/device", v.GetString(targetURLKey), apiBase),
		WRPSource:   v.GetString(WRPSourcekey),
		CtxTimeout:  tConfigs.rTimeout,
		Do: xhttp.RetryTransactor(xhttp.RetryOptions{
			Logger:   logger,
			Retries:  v.GetInt(reqMaxRetriesKey),
			Interval: v.GetDuration(reqRetryIntervalKey),
		}, newClient(v, tConfigs).Do),
	})

	translation.ConfigHandler(&translation.Options{
		S:             ts,
		APIRouter:     APIRouter,
		Authenticate:  authenticate,
		Log:           logger,
		ValidServices: v.GetStringSlice(translationServicesKey),
	})

	var (
		_, tr1d1umServer = webPA.Prepare(logger, nil, metricsRegistry, r)
		signals          = make(chan os.Signal, 1)
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
		snsFactory.PrepareAndStart()
	}

	signal.Notify(signals)
	s := server.SignalWait(infoLogger, signals, os.Kill, os.Interrupt)
	errorLogger.Log(logging.MessageKey(), "exiting due to signal", "signal", s)
	close(shutdown)
	waitGroup.Wait()

	return 0
}

//timeoutConfigs holds parsable config values for HTTP transactions
type timeoutConfigs struct {
	//HTTP client timeout
	cTimeout time.Duration

	//HTTP request timeout
	rTimeout time.Duration

	//net dialer timeout
	dTimeout time.Duration
}

func newTimeoutConfigs(v *viper.Viper) (t *timeoutConfigs, err error) {
	var c, r, d time.Duration
	if c, err = time.ParseDuration(v.GetString(clientTimeoutKey)); err == nil {
		if r, err = time.ParseDuration(v.GetString(reqTimeoutKey)); err == nil {
			if d, err = time.ParseDuration(v.GetString(netDialerTimeoutKey)); err == nil {
				t = &timeoutConfigs{
					cTimeout: c,
					rTimeout: r,
					dTimeout: d,
				}
			}
		}
	}
	return
}

func newClient(v *viper.Viper, t *timeoutConfigs) *http.Client {
	return &http.Client{
		Timeout: t.cTimeout,
		Transport: &http.Transport{
			Dial: (&net.Dialer{
				Timeout: t.dTimeout,
			}).Dial},
	}
}

//authenticationHandler configures the authorization requirements for requests to reach the main handler
func authenticationHandler(v *viper.Viper, logger log.Logger, registry xmetrics.Registry) (preHandler *alice.Chain, err error) {
	m := secure.NewJWTValidationMeasures(registry)
	var validator secure.Validator
	if validator, err = getValidator(v, m); err == nil {

		authHandler := handler.AuthorizationHandler{
			HeaderName:          "Authorization",
			ForbiddenStatusCode: 403,
			Validator:           validator,
			Logger:              logger,
		}

		authHandler.DefineMeasures(m)

		newPreHandler := alice.New(authHandler.Decorate)
		preHandler = &newPreHandler
	}
	return
}

//getValidator returns a validator for JWT/Basic tokens
//It reads in tokens from a config file. Zero or more tokens can be read.
func getValidator(v *viper.Viper, m *secure.JWTValidationMeasures) (validator secure.Validator, err error) {
	var jwtVals []struct {
		Keys   key.ResolverFactory        `json:"keys"`
		Custom secure.JWTValidatorFactory `json:"custom"`
	}

	v.UnmarshalKey("jwtValidators", &jwtVals)

	// if a JWTKeys section was supplied, configure a JWS validator
	// and append it to the chain of validators
	validators := make(secure.Validators, 0, len(jwtVals))

	for _, validatorDescriptor := range jwtVals {
		validatorDescriptor.Custom.DefineMeasures(m)

		var keyResolver key.Resolver
		keyResolver, err = validatorDescriptor.Keys.NewResolver()
		if err != nil {
			validator = validators
			return
		}

		validator := secure.JWSValidator{
			DefaultKeyId:  DefaultKeyID,
			Resolver:      keyResolver,
			JWTValidators: []*jwt.Validator{validatorDescriptor.Custom.New()},
		}

		validator.DefineMeasures(m)
		validators = append(validators, validator)
	}

	basicAuth := v.GetStringSlice("authHeader")
	for _, authValue := range basicAuth {
		validators = append(
			validators,
			secure.ExactMatchValidator(authValue),
		)
	}

	validator = validators

	return
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
