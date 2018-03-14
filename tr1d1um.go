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

	"github.com/Comcast/tr1d1um/translation"
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
	applicationName, apiBase = "tr1d1um", "/api/v2"

	defaultClientTimeout    = "30s"
	defaultRespWaitTimeout  = "40s"
	defaultNetDialerTimeout = "5s"
	defaultRetryInterval    = "2s"
	defaultMaxRetries       = 2
	defaultTargetURL        = "localhost:6000"
	defaultWRPSource        = "dns:localhost"

	supportedServicesKey = "supportedServices"
	targetURLKey         = "targetURL"
	netDialerTimeoutKey  = "netDialerTimeout"
	clientTimeoutKey     = "clientTimeout"
	reqRetryIntervalKey  = "requestRetryInterval"
	reqMaxRetriesKey     = "requestMaxRetries"
	respWaitTimeoutKey   = "respWaitTimeout"
	WRPSourcekey         = "WRPSource"
)

func tr1d1um(arguments []string) (exitCode int) {

	var (
		f                                   = pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v                                   = viper.New()
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
	infoLogger.Log("configurationFile", v.ConfigFileUsed())

	r := mux.NewRouter()

	baseRouter := r.PathPrefix(apiBase).Subrouter()

	authenticate, err = authenticationHandler(v, logger, metricsRegistry)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to build authentication handler: %s\n", err.Error())
		return 1
	}

	service := prepareWRPService(v)

	translation.ConfigHandler(service, baseRouter, authenticate, logger)

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

	signal.Notify(signals)
	s := server.SignalWait(infoLogger, signals, os.Kill, os.Interrupt)
	errorLogger.Log(logging.MessageKey(), "exiting due to signal", "signal", s)
	close(shutdown)
	waitGroup.Wait()

	return 0
}

func prepareWRPService(v *viper.Viper) translation.Service {
	clientTimeout, _ := time.ParseDuration(v.GetString(clientTimeoutKey))
	respTimeout, _ := time.ParseDuration(v.GetString(respWaitTimeoutKey))
	dialerTimeout, _ := time.ParseDuration(v.GetString(netDialerTimeoutKey))
	maxRetries := v.GetInt(reqMaxRetriesKey)

	client := &http.Client{
		Timeout: clientTimeout,
		Transport: &http.Transport{
			Dial: (&net.Dialer{
				Timeout: dialerTimeout,
			}).Dial},
	}

	return &translation.WRPService{
		RetryDo: xhttp.RetryTransactor(xhttp.RetryOptions{
			Retries: maxRetries}, client.Do),
		XmidtURL:   fmt.Sprintf("%s%s/device", v.GetString(targetURLKey), apiBase),
		WRPSource:  v.GetString(WRPSourcekey),
		CtxTimeout: respTimeout,
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

func injectDefaults(v *viper.Viper) {
	v.SetDefault(clientTimeoutKey, defaultClientTimeout)
	v.SetDefault(respWaitTimeoutKey, defaultRespWaitTimeout)
	v.SetDefault(reqRetryIntervalKey, defaultRetryInterval)
	v.SetDefault(reqMaxRetriesKey, defaultMaxRetries)
	v.SetDefault(netDialerTimeoutKey, defaultNetDialerTimeout)
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
