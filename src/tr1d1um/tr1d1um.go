package main

import (
	"net/http"
	"os"
	"time"
	"fmt"
	"os/signal"

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
	"github.com/Comcast/webpa-common/concurrent"
)

const (
	applicationName = "tr1d1um"
	DefaultKeyId    = "current"
	baseURI = "/api"
	version = "v2"// TODO: Should these values change?
)

func tr1d1um(arguments []string) int {
	var (
		f              = pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
		v              = viper.New()
		logger, webPA , err = server.Initialize(applicationName, arguments, f, v)
	)
	if err != nil {
		logging.Error(logger).Log(logging.MessageKey(), "Unable to initialize Viper environment",
			logging.ErrorKey(), err)
		fmt.Fprint(os.Stderr, "Unable to initialize viper" + err.Error())
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

		fmt.Fprint(os.Stderr, "Unable to unmarshall config")
		return 1
	}

	preHandler, err := SetUpPreHandler(v, logger)

	if err != nil {
		infoLogger.Log(messageKey, "Error setting up pre handler", errorKey, err)

		fmt.Fprint(os.Stderr, "error setting up prehandler")
		return 1
	}

	conversionHandler := SetUpHandler(tConfig, logger)

	AddRoutes(preHandler, conversionHandler)

	_, tr1d1umServer := webPA.Prepare(logger, nil, conversionHandler)

	waitGroup, shutdown, err := concurrent.Execute(tr1d1umServer)

	signals  := make(chan os.Signal, 1)

	signal.Notify(signals)
	<-signals
	close(shutdown)

	waitGroup.Wait()

	return 0
}

func AddRoutes(preHandler *alice.Chain, conversionHandler *ConversionHandler) {
	var BodyNonNil = func(request *http.Request, match *mux.RouteMatch) bool {
		return request.Body != nil
	}

	r := mux.NewRouter()
	apiHandler := r.PathPrefix(fmt.Sprintf("%s/%s", baseURI, version)).Subrouter()

	apiHandler.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodGet)

	apiHandler.Handle("/device/{deviceid}/{service}", preHandler.Then(conversionHandler)).
		Methods(http.MethodPatch).MatcherFunc(BodyNonNil)

	apiHandler.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
	Methods(http.MethodDelete)

	apiHandler.Handle("/device/{deviceid}/{service}/{parameter}", preHandler.Then(conversionHandler)).
	Methods(http.MethodPut, http.MethodPost).MatcherFunc(BodyNonNil)
}

func SetUpHandler(tConfig *Tr1d1umConfig, logger log.Logger) (cHandler *ConversionHandler) {
	timeOut, err := time.ParseDuration(tConfig.HttpTimeout)
	if err != nil {
		timeOut = time.Second * 60 //default val
	}
	cHandler = &ConversionHandler{
		timeOut: timeOut,
		targetURL: tConfig.targetURL,
		wdmpConvert: &ConversionWDMP{&EncodingHelper{}},
		sender: &Tr1SendAndHandle{log:logger, timedClient:&http.Client{Timeout:time.Second*5}, NewHTTPRequest:http.NewRequest},
		encodingHelper:&EncodingHelper{},
		}
	//pass loggers
	cHandler.errorLogger = logging.Error(logger)
	cHandler.infoLogger = logging.Info(logger)
	cHandler.targetURL = "https://xmidt.comcast.net" //todo: should we get this from the configs instead?
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

//GetValidator returns a validator for JWT tokens
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

func main() {
	os.Exit(tr1d1um(os.Args))
}
