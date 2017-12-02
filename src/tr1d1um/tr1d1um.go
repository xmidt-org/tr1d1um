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
	"bytes"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/handler"
	"github.com/Comcast/webpa-common/secure/key"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/webhook"
	"github.com/SermoDigital/jose/jwt"
	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/metrics"
	"github.com/go-kit/kit/metrics/provider"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

//convenient global values
const (
	applicationName = "tr1d1um"
	baseURI         = "/api"

	DefaultKeyID            = "current"
	defaultClientTimeout    = "30s"
	defaultRespWaitTimeout  = "40s"
	defaultNetDialerTimeout = "5s"
	defaultRetryInterval    = "2s"
	defaultMaxRetries       = 2

	supportedServicesKey = "supportedServices"
	targetURLKey         = "targetURL"
	netDialerTimeoutKey  = "netDialerTimeout"
	clientTimeoutKey     = "clientTimeout"
	reqRetryIntervalKey  = "requestRetryInterval"
	reqMaxRetriesKey     = "requestMaxRetries"
	respWaitTimeoutKey   = "respWaitTimeout"

	releaseKey = "release"
)

var (
	release                = "-"
	hostname               = "-"
	requetsReceivedCounter metrics.Counter
)

func tr1d1um(arguments []string) (exitCode int) {

	var (
		f                  = pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v                  = viper.New()
		logger, webPA, err = server.Initialize(applicationName, arguments, f, v)
	)
	// set config file value defaults
	v.SetDefault(clientTimeoutKey, defaultClientTimeout)
	v.SetDefault(respWaitTimeoutKey, defaultRespWaitTimeout)
	v.SetDefault(reqRetryIntervalKey, defaultRetryInterval)
	v.SetDefault(reqMaxRetriesKey, defaultMaxRetries)
	v.SetDefault(netDialerTimeoutKey, defaultNetDialerTimeout)

	//release and internal OS info set up
	if releaseVal := v.GetString(releaseKey); releaseVal != "" {
		release = releaseVal
	}

	if hostnameVal, hostnameErr := os.Hostname(); hostnameErr == nil {
		hostname = hostnameVal
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize viper: %s\n", err.Error())
		return 1
	}

	var (
		infoLogger = logging.Info(logger)
	)

	infoLogger.Log("configurationFile", v.ConfigFileUsed())

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to unmarshall config data into struct: %s\n", err.Error())
		return 1
	}

	preHandler, err := SetUpPreHandler(v, logger)

	if err != nil {
		fmt.Fprintf(os.Stderr, "error setting up prehandler: %s\n", err.Error())
		return 1
	}

	conversionHandler := SetUpHandler(v, logger)

	r := mux.NewRouter()
	baseRouter := r.PathPrefix(fmt.Sprintf("%s/%s", baseURI, v.GetString("version"))).Subrouter()

	AddRoutes(baseRouter, preHandler, conversionHandler)

	var snsFactory *webhook.Factory

	if snsFactory, exitCode = ConfigureWebHooks(baseRouter, r, preHandler, v, logger); exitCode != 0 {
		return
	}

	var (
		_, tr1d1umServer = webPA.Prepare(logger, nil, r)
		signals          = make(chan os.Signal, 1)
	)

	//todo: just example usage for now of new webpa server metrics
	//initialize the metrics provider
	webPA.GoKitMetricsProvider = provider.NewPrometheusProvider(applicationName, "todo") //todo: what's the approapiate subsystem?

	//create a counter
	requetsReceivedCounter = webPA.GoKitMetricsProvider.NewCounter("requests_received")

	go snsFactory.PrepareAndStart()

	if err := concurrent.Await(tr1d1umServer, signals); err != nil {
		fmt.Fprintf(os.Stderr, "Error when starting %s: %s", applicationName, err)
		return 4
	}

	return 0
}

//ConfigureWebHooks sets route paths, initializes and synchronizes hook registries for this tr1d1um instance
//baseRouter is pre-configured with the api/v2 prefix path
//root is the original router used by webHookFactory.Initialize()
func ConfigureWebHooks(baseRouter *mux.Router, root *mux.Router, preHandler *alice.Chain, v *viper.Viper, logger log.Logger) (*webhook.Factory, int) {
	webHookFactory, err := webhook.NewFactory(v)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating new webHook factory: %s\n", err)
		return nil, 1
	}

	webHookRegistry, webHookHandler := webHookFactory.NewRegistryAndHandler()

	// register webHook end points for api
	baseRouter.Handle("/hook", preHandler.ThenFunc(webHookRegistry.UpdateRegistry))
	baseRouter.Handle("/hooks", preHandler.ThenFunc(webHookRegistry.GetRegistry))

	selfURL := &url.URL{
		Scheme: "https",
		Host:   v.GetString("fqdn") + v.GetString("primary.address"),
	}

	webHookFactory.Initialize(root, selfURL, webHookHandler, logger, nil)
	return webHookFactory, 0
}

//AddRoutes configures the paths and connection rules to TR1D1UM
func AddRoutes(r *mux.Router, preHandler *alice.Chain, conversionHandler *ConversionHandler) {
	var BodyNonEmpty = func(request *http.Request, match *mux.RouteMatch) (accept bool) {
		if request.Body != nil {
			var tmp bytes.Buffer
			p, err := ioutil.ReadAll(request.Body)
			if accept = err == nil && len(p) > 0; accept {
				//place back request's body
				tmp.Write(p)
				request.Body = ioutil.NopCloser(&tmp)
			}
		}
		return
	}

	r.Handle("/device/{deviceid}/stat", preHandler.ThenFunc(conversionHandler.HandleStat)).
		Methods(http.MethodGet)

	r.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodGet)

	r.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodPatch)

	r.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
		Methods(http.MethodDelete)

	r.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
		Methods(http.MethodPut, http.MethodPost).MatcherFunc(BodyNonEmpty)
}

//SetUpHandler prepares the main handler under TR1D1UM which is the ConversionHandler
func SetUpHandler(v *viper.Viper, logger log.Logger) (cHandler *ConversionHandler) {
	clientTimeout, _ := time.ParseDuration(v.GetString(clientTimeoutKey))
	respTimeout, _ := time.ParseDuration(v.GetString(respWaitTimeoutKey))
	retryInterval, _ := time.ParseDuration(v.GetString(reqRetryIntervalKey))
	dialerTimeout, _ := time.ParseDuration(v.GetString(netDialerTimeoutKey))
	maxRetries := v.GetInt(reqMaxRetriesKey)

	cHandler = &ConversionHandler{
		WdmpConvert: &ConversionWDMP{
			encodingHelper: &EncodingHelper{},
			WRPSource:      v.GetString("WRPSource")},
		Sender: SendAndHandleFactory{}.New(respTimeout,
			&ContextTimeoutRequester{&http.Client{Timeout: clientTimeout,
				Transport: &http.Transport{
					Dial: (&net.Dialer{
						Timeout: dialerTimeout,
					}).Dial}},
			}, &EncodingHelper{}, logger),
		EncodingHelper: &EncodingHelper{},
		Logger:         logger,
		RequestValidator: &TR1RequestValidator{
			supportedServices: getSupportedServicesMap(v.GetStringSlice(supportedServicesKey)),
			Logger:            logger,
		},
		RetryStrategy: RetryStrategyFactory{}.NewRetryStrategy(logger, retryInterval, maxRetries,
			ShouldRetryOnResponse, OnRetryInternalFailure),
		WRPRequestURL: fmt.Sprintf("%s%s/%s/device", v.GetString(targetURLKey), baseURI, v.GetString("version")),
		TargetURL:     v.GetString(targetURLKey),
	}

	return
}

//SetUpPreHandler configures the authorization requirements for requests to reach the main handler
func SetUpPreHandler(v *viper.Viper, logger log.Logger) (preHandler *alice.Chain, err error) {
	validator, err := GetValidator(v)
	if err != nil {
		return
	}

	authHandler := handler.AuthorizationHandler{
		HeaderName:          "Authorization",
		ForbiddenStatusCode: 403,
		Validator:           validator,
		Logger:              logger,
	}

	newPreHandler := alice.New(authHandler.Decorate)
	preHandler = &newPreHandler
	return
}

//GetValidator returns a validator for JWT tokens
func GetValidator(v *viper.Viper) (validator secure.Validator, err error) {
	defaultValidators := make(secure.Validators, 0, 0)
	var jwtVals []JWTValidator

	v.UnmarshalKey("jwtValidators", &jwtVals)

	// make sure there is at least one jwtValidator supplied
	if len(jwtVals) < 1 {
		validator = defaultValidators
		return
	}

	// if a JWTKeys section was supplied, configure a JWS validator
	// and append it to the chain of validators
	validators := make(secure.Validators, 0, len(jwtVals))

	for _, validatorDescriptor := range jwtVals {
		var keyResolver key.Resolver
		keyResolver, err = validatorDescriptor.Keys.NewResolver()
		if err != nil {
			validator = validators
			return
		}

		validators = append(
			validators,
			secure.JWSValidator{
				DefaultKeyId:  DefaultKeyID,
				Resolver:      keyResolver,
				JWTValidators: []*jwt.Validator{validatorDescriptor.Custom.New()},
			},
		)
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

func getSupportedServicesMap(supportedServices []string) (supportedServicesMap map[string]struct{}) {
	supportedServicesMap = map[string]struct{}{}
	if supportedServices != nil {
		for _, supportedService := range supportedServices {
			supportedServicesMap[supportedService] = struct{}{}
		}
	}
	return
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
