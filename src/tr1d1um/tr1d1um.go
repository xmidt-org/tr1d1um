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
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	_ "net/http/pprof"
	"os"
	"os/signal"
	"time"

	"tr1d1um/common"
	"tr1d1um/hooks"
	"tr1d1um/stat"
	"tr1d1um/translation"

	"github.com/Comcast/comcast-bascule/bascule"
	"github.com/Comcast/comcast-bascule/bascule/basculehttp"
	"github.com/Comcast/comcast-bascule/bascule/key"
	"github.com/goph/emperror"

	"github.com/Comcast/webpa-common/basculechecks"
	"github.com/Comcast/webpa-common/xhttp"

	"github.com/Comcast/webpa-common/concurrent"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
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
	applicationVersion     = "0.2.0"
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
		logger, metricsRegistry, webPA, err = server.Initialize(applicationName, arguments, f, v, webhook.Metrics, aws.Metrics, basculechecks.Metrics)
	)

	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to initialize viper: %s\n", err.Error())
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

	r := mux.NewRouter()

	r.NotFoundHandler = http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	})

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

	if v.GetBool("webhooksEnabled") {
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
		Tr1d1umTransactor: common.NewTr1d1umTransactor(
			&common.Tr1d1umTransactorOptions{
				Do: xhttp.RetryTransactor(
					xhttp.RetryOptions{
						Logger:   logger,
						Retries:  v.GetInt(reqMaxRetriesKey),
						Interval: v.GetDuration(reqRetryIntervalKey),
					},
					newClient(v, tConfigs).Do),
				RequestTimeout: tConfigs.rTimeout,
			}),
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

		WRPSource: v.GetString(WRPSourcekey),

		Tr1d1umTransactor: common.NewTr1d1umTransactor(
			&common.Tr1d1umTransactorOptions{
				RequestTimeout: tConfigs.rTimeout,
				Do: xhttp.RetryTransactor(
					xhttp.RetryOptions{
						Logger:   logger,
						Retries:  v.GetInt(reqMaxRetriesKey),
						Interval: v.GetDuration(reqRetryIntervalKey),
					},
					newClient(v, tConfigs).Do),
			}),
	})

	translation.ConfigHandler(&translation.Options{
		S:             ts,
		APIRouter:     APIRouter,
		Authenticate:  authenticate,
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

func SetLogger(logger log.Logger) func(delegate http.Handler) http.Handler {
	return func(delegate http.Handler) http.Handler {
		return http.HandlerFunc(
			func(w http.ResponseWriter, r *http.Request) {
				ctx := r.WithContext(logging.WithLogger(r.Context(),
					log.With(logger, "requestHeaders", r.Header, "requestURL", r.URL.EscapedPath(), "method", r.Method)))
				delegate.ServeHTTP(w, ctx)
			})
	}
}

func GetLogger(ctx context.Context) bascule.Logger {
	logger := log.With(logging.GetLogger(ctx), "ts", log.DefaultTimestampUTC, "caller", log.DefaultCaller)
	return logger
}

//JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory `json:"keys"`

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory `json:"custom"`
}

//authenticationHandler configures the authorization requirements for requests to reach the main handler
func authenticationHandler(v *viper.Viper, logger log.Logger, registry xmetrics.Registry) (*alice.Chain, error) {

	var (
		m *basculechecks.JWTValidationMeasures
	)

	if registry != nil {
		m = basculechecks.NewJWTValidationMeasures(registry)
	}
	listener := basculechecks.NewMetricListener(m)

	basicAllowed := make(map[string]string)
	basicAuth := v.GetStringSlice("authHeader")
	for _, a := range basicAuth {
		decoded, err := base64.StdEncoding.DecodeString(a)
		if err != nil {
			logging.Info(logger).Log(logging.MessageKey(), "failed to decode auth header", "authHeader", a, logging.ErrorKey(), err.Error())
		}

		i := bytes.IndexByte(decoded, ':')
		logging.Debug(logger).Log(logging.MessageKey(), "decoded string", "string", decoded, "i", i)
		if i > 0 {
			basicAllowed[string(decoded[:i])] = string(decoded[i+1:])
		}
	}
	logging.Debug(logger).Log(logging.MessageKey(), "Created list of allowed basic auths", "allowed", basicAllowed, "config", basicAuth)

	options := []basculehttp.COption{basculehttp.WithCLogger(GetLogger), basculehttp.WithCErrorResponseFunc(listener.OnErrorResponse)}
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
			DefaultKeyId:  DefaultKeyID,
			Resolver:      resolver,
			Parser:        bascule.DefaultJWSParser,
			JWTValidators: []*jwt.Validator{jwtVal.Custom.New()},
		}))
	}

	authConstructor := basculehttp.NewConstructor(options...)

	bearerRules := []bascule.Validator{
		bascule.CreateNonEmptyPrincipalCheck(),
		bascule.CreateNonEmptyTypeCheck(),
		bascule.CreateValidTypeCheck([]string{"jwt"}),
	}

	// only add capability check if the configuration is set
	var capabilityConfig basculechecks.CapabilityConfig
	v.UnmarshalKey("capabilityConfig", &capabilityConfig)
	if capabilityConfig.FirstPiece != "" && capabilityConfig.SecondPiece != "" && capabilityConfig.ThirdPiece != "" {
		bearerRules = append(bearerRules, bascule.CreateListAttributeCheck("capabilities", basculechecks.CreateValidCapabilityCheck(capabilityConfig)))
	}

	authEnforcer := basculehttp.NewEnforcer(
		basculehttp.WithELogger(GetLogger),
		basculehttp.WithRules("Basic", []bascule.Validator{
			bascule.CreateAllowAllCheck(),
		}),
		basculehttp.WithRules("Bearer", bearerRules),
		basculehttp.WithEErrorResponseFunc(listener.OnErrorResponse),
	)

	chain := alice.New(SetLogger(logger), authConstructor, authEnforcer, basculehttp.NewListenerDecorator(listener))
	return &chain, nil
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
