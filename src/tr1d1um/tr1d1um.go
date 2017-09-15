package main

import (
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/secure"
	"github.com/Comcast/webpa-common/secure/handler"
	"github.com/Comcast/webpa-common/secure/key"
	"github.com/Comcast/webpa-common/server"
	"github.com/SermoDigital/jose/jwt"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"net/http"
	"os"
	"time"
)

const (
	applicationName = "tr1d1um"
	DefaultKeyId    = "current"
)

func tr1d1um(arguments []string) int {
	var (
		f              = pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v              = viper.New()
		logger, _, err = server.Initialize(applicationName, arguments, f, v)
	)

	if err != nil {
		logging.Error(logger).Log(logging.MessageKey(), "Unable to initialize Viper environment: %s\n",
			logging.ErrorKey(), err)
		return 1
	}

	var (
		messageKey  = logging.MessageKey()
		errorKey    = logging.ErrorKey()
		infoLogger  = logging.Info(logger)
		errorLogger = logging.Error(logger)
	)

	infoLogger.Log("configurationFile", v.ConfigFileUsed())

	tConfig := new(Tr1d1umConfig)
	err = v.Unmarshal(tConfig) //todo: decide best way to get current unexported fields from viper
	if err != nil {
		errorLogger.Log(messageKey, "Unable to unmarshal configuration data into struct", errorKey, err)
		return 1
	}

	preHandler, err := SetUpPreHandler(v, logger)

	if err != nil {
		infoLogger.Log(messageKey, "Error setting up pre handler", errorKey, err)
		return 1
	}

	conversionHandler := SetUpHandler(tConfig, errorLogger, infoLogger)

	r := mux.NewRouter()

	r = AddRoutes(r, preHandler, conversionHandler)

	//todo: finish this initialization method

	return 0
}

func AddRoutes(r *mux.Router, preHandler *alice.Chain, conversionHandler *ConversionHandler) *mux.Router {
	var BodyNonNil = func(request *http.Request, match *mux.RouteMatch) bool {
		return request.Body != nil
	}

	//todo: inquire about API version
	r.Handle("/device/{deviceid}/{service}", preHandler.ThenFunc(conversionHandler.ConversionGETHandler)).
		Methods(http.MethodGet)

	r.Handle("/device/{deviceid}/{service}", preHandler.ThenFunc(conversionHandler.ConversionSETHandler)).
		Methods(http.MethodPatch).MatcherFunc(BodyNonNil)

	r.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.ThenFunc(conversionHandler.
		ConversionDELETEHandler)).Methods(http.MethodDelete)

	r.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.ThenFunc(conversionHandler.
		ConversionADDHandler)).Methods(http.MethodPost).MatcherFunc(BodyNonNil)

	r.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.ThenFunc(conversionHandler.
		ConversionREPLACEHandler)).Methods(http.MethodPut).MatcherFunc(BodyNonNil)

	return r
}

// getValidator returns validator for JWT tokens
func GetValidator(v *viper.Viper) (validator secure.Validator, err error) {
	default_validators := make(secure.Validators, 0, 0)
	var jwtVals []JWTValidator

	v.UnmarshalKey("jwtValidators", &jwtVals)

	// make sure there is at least one jwtValidator supplied
	if len(jwtVals) < 1 {
		validator = default_validators
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
				DefaultKeyId:  DefaultKeyId,
				Resolver:      keyResolver,
				JWTValidators: []*jwt.Validator{validatorDescriptor.Custom.New()},
			},
		)
	}

	// TODO: This should really be part of the unmarshalled validators somehow
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

func SetUpHandler(tConfig *Tr1d1umConfig, errorLogger log.Logger, infoLogger log.Logger) (cHandler *ConversionHandler) {
	cHandler = &ConversionHandler{timeOut: time.Duration(tConfig.timeOut), targetUrl: tConfig.targetUrl}
	//pass loggers
	cHandler.errorLogger = errorLogger
	cHandler.infoLogger = infoLogger
	//set functions
	cHandler.GetFlavorFormat = GetFlavorFormat
	cHandler.SetFlavorFormat = SetFlavorFormat
	cHandler.DeleteFlavorFormat = DeleteFlavorFormat
	cHandler.ReplaceFlavorFormat = ReplaceFlavorFormat
	cHandler.AddFlavorFormat = AddFlavorFormat

	return
}

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

func main() {
	os.Exit(tr1d1um(os.Args))
}
