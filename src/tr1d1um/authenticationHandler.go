package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"net/http"

	"github.com/Comcast/comcast-bascule/bascule"
	"github.com/Comcast/comcast-bascule/bascule/basculehttp"
	"github.com/Comcast/comcast-bascule/bascule/key"
	"github.com/Comcast/webpa-common/basculechecks"
	"github.com/Comcast/webpa-common/secure"
	"github.com/SermoDigital/jose/jwt"
	"github.com/goph/emperror"
	"github.com/justinas/alice"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/xmetrics"
	"github.com/go-kit/kit/log"
	"github.com/spf13/viper"
)

type JWTValidator struct {
	// JWTKeys is used to create the key.Resolver for JWT verification keys
	Keys key.ResolverFactory `json:"keys"`

	// Custom is an optional configuration section that defines
	// custom rules for validation over and above the standard RFC rules.
	Custom secure.JWTValidatorFactory `json:"custom"`
}

//authenticationHandler configures the authorization requirements for requests to reach the main handler
func NewAuthenticationHandler(v *viper.Viper, logger log.Logger, registry xmetrics.Registry) (*alice.Chain, error) {

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
