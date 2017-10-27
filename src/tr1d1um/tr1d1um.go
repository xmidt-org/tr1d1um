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
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

//convenient global values
const (
	applicationName        = "tr1d1um"
	DefaultKeyID           = "current"
	baseURI                = "/api"
	defaultClientTimeout   = "30s"
	defaultRespWaitTimeout = "40s"
)

func tr1d1um(arguments []string) (exitCode int) {

	var (
		f                  = pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v                  = viper.New()
		logger, webPA, err = server.Initialize(applicationName, arguments, f, v)
	)
	//timeout defaults: //TODO: maybe this should be a common default among all xmidt/webpa servers
	v.SetDefault("clientTimeout", defaultClientTimeout)
	v.SetDefault("respWaitTimeout", defaultRespWaitTimeout)

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

	AddRoutes(r, preHandler, conversionHandler, v)

	if exitCode = ConfigureWebHooks(r, preHandler, v, logger); exitCode != 0 {
		return
	}

	var (
		_, tr1d1umServer = webPA.Prepare(logger, nil, r)
		signals          = make(chan os.Signal, 1)
	)

	if err := concurrent.Await(tr1d1umServer, signals); err != nil {
		fmt.Fprintf(os.Stderr, "Error when starting %s: %s", applicationName, err)
		return 4
	}

	return 0
}

//ConfigureWebHooks sets route paths, initializes and synchronizes hook registries for this tr1d1um instance
func ConfigureWebHooks(r *mux.Router, preHandler *alice.Chain, v *viper.Viper, logger log.Logger) int {
	webHookFactory, err := webhook.NewFactory(v)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating new webHook factory: %s\n", err)
		return 1
	}

	webHookRegistry, webHookHandler := webHookFactory.NewRegistryAndHandler()

	// register webHook end points for api
	r.Handle("/hook", preHandler.ThenFunc(webHookRegistry.UpdateRegistry))
	r.Handle("/hooks", preHandler.ThenFunc(webHookRegistry.GetRegistry))

	selfURL := &url.URL{
		Scheme: "https",
		Host:   v.GetString("fqdn") + v.GetString("primary.address"),
	}

	webHookFactory.Initialize(r, selfURL, webHookHandler, logger, nil)
	webHookFactory.PrepareAndStart()

	return 0
}

//AddRoutes configures the paths and connection rules to TR1D1UM
func AddRoutes(r *mux.Router, preHandler *alice.Chain, conversionHandler *ConversionHandler, v *viper.Viper) {
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

	apiHandler := r.PathPrefix(fmt.Sprintf("%s/%s", baseURI, v.GetString("version"))).Subrouter()

	apiHandler.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodGet)

	apiHandler.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodPatch).MatcherFunc(BodyNonEmpty)

	apiHandler.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
		Methods(http.MethodDelete)

	apiHandler.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
		Methods(http.MethodPut, http.MethodPost).MatcherFunc(BodyNonEmpty)
}

//SetUpHandler prepares the main handler under TR1D1UM which is the ConversionHandler
func SetUpHandler(v *viper.Viper, logger log.Logger) (cHandler *ConversionHandler) {
	clientTimeout, _ := time.ParseDuration(v.GetString("clientTimeout"))
	respTimeout, _ := time.ParseDuration(v.GetString("respWaitTimeout"))

	cHandler = &ConversionHandler{
		wdmpConvert: &ConversionWDMP{&EncodingHelper{}},
		//TODO: add needed elements into http.Client
		sender: &Tr1SendAndHandle{client: &http.Client{Timeout: clientTimeout}, log: logger,
			NewHTTPRequest: http.NewRequest, respTimeout: respTimeout},
		encodingHelper: &EncodingHelper{}, logger: logger,
		targetURL:     v.GetString("targetURL"),
		serverVersion: v.GetString("version"),
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

func main() {
	os.Exit(tr1d1um(os.Args))
}
