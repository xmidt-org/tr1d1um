/**
 * Copyright 2021 Comcast Cable Communications Management, LLC
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
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"regexp"
	"time"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/goph/emperror"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/acquire"
	bchecks "github.com/xmidt-org/bascule/basculechecks"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/key"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/webpa-common/v2/basculechecks"
	"github.com/xmidt-org/webpa-common/v2/basculemetrics"
	"github.com/xmidt-org/webpa-common/v2/logging"
	"github.com/xmidt-org/webpa-common/v2/xmetrics"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

var (
	errFailedWebhookUnmarshal = errors.New("failed to JSON unmarshal webhook")

	v2WarningHeader = "X-Xmidt-Warning"
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

	authConstructor := basculehttp.NewConstructor(append([]basculehttp.COption{
		basculehttp.WithParseURLFunc(basculehttp.CreateRemovePrefixURLFunc("/"+apiBase+"/", basculehttp.DefaultParseURLFunc)),
	}, options...)...)
	authConstructorLegacy := basculehttp.NewConstructor(append([]basculehttp.COption{
		basculehttp.WithParseURLFunc(basculehttp.CreateRemovePrefixURLFunc("/api/"+prevAPIVersion+"/", basculehttp.DefaultParseURLFunc)),
		basculehttp.WithCErrorHTTPResponseFunc(basculehttp.LegacyOnErrorHTTPResponse),
	}, options...)...)

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

	authChain := alice.New(setLogger(logger), authConstructor, authEnforcer, basculehttp.NewListenerDecorator(listener))
	authChainLegacy := alice.New(setLogger(logger), authConstructorLegacy, authEnforcer, basculehttp.NewListenerDecorator(listener))

	versionCompatibleAuth := alice.New(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(r http.ResponseWriter, req *http.Request) {
			vars := mux.Vars(req)
			if vars != nil {
				if vars["version"] == prevAPIVersion {
					authChainLegacy.Then(next).ServeHTTP(r, req)
					return
				}
			}
			authChain.Then(next).ServeHTTP(r, req)
		})
	})
	return &versionCompatibleAuth, nil
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
