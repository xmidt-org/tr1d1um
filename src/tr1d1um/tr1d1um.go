package main

import (
	"os"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/Comcast/webpa-common/server"
	"github.com/Comcast/webpa-common/logging"
	"github.com/gorilla/mux"
	"github.com/Comcast/webpa-common/secure/handler"
	"github.com/Comcast/webpa-common/secure"
	"github.com/SermoDigital/jose/jwt"
	"github.com/justinas/alice"
	"github.com/Comcast/webpa-common/secure/key"
)

const (
	applicationName = "tr1d1um"
	DefaultKeyId = "current"
)
var tConfig Tr1d1umConfig

func tr1d1um(arguments []string) int {

	var (
		f= pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v= viper.New()
		logger, _, err= server.Initialize(applicationName, arguments, f, v)
	)

	if err != nil {
		logging.Error(logger).Log(logging.MessageKey(), "Unable to initialize Viper environment: %s\n",
			logging.ErrorKey(), err)
		return 1
	}

	var (
		messageKey = logging.MessageKey()
		errorKey = logging.ErrorKey()
		infoLog= logging.Info(logger)
		errorLog= logging.Error(logger)
	)

	infoLog.Log("configurationFile", v.ConfigFileUsed())

	tConfig := new(Tr1d1umConfig)
	err = v.Unmarshal(tConfig)
	if err != nil {
		errorLog.Log(messageKey,"Unable to unmarshal configuration data into struct", errorKey, err)
		return 1
	}

	r := mux.NewRouter()

	validator, err := getValidator(v)
	if err != nil {
		infoLog.Log(messageKey,"Error retrieving validator from configs", "configFile", v.ConfigFileUsed())
		return 1
	}

	authHandler := handler.AuthorizationHandler{
		HeaderName:          "Authorization",
		ForbiddenStatusCode: 403,
		Validator:           validator,
		Logger:              logger,
	}

	tHandler := alice.New(authHandler.Decorate)

	r = AddRoutes(r, &tHandler)

	//todo: finish this initialization method

	return 0
}

func AddRoutes(r *mux.Router, h *alice.Chain) (* mux.Router) {
	//todo: path will change later
	//todo: add restrictions

	//todo: configure handler path correctly
	r.Handle("/device/", h.ThenFunc(ConversionHandler))
	return r
}

// getValidator returns validator for JWT tokens
//todo will probably change to use go-kit
func getValidator(v *viper.Viper) (validator secure.Validator, err error) {
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

func main() {
	os.Exit(tr1d1um(os.Args))
}

