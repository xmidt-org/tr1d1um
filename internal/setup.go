// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package tr1d1um

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/xmidt-org/sallust"
	"go.uber.org/zap"
)

// convenient global values
const (
	DefaultKeyID       = "current"
	apiVersion         = "v3"
	prevAPIVersion     = "v2"
	ApplicationName    = "tr1d1um"
	apiBase            = "api/" + apiVersion
	prevAPIBase        = "api/" + prevAPIVersion
	apiBaseDualVersion = "api/{version:" + apiVersion + "|" + prevAPIVersion + "}"
)

const (
	translationServicesKey            = "supportedServices"
	targetURLKey                      = "targetURL"
	netDialerTimeoutKey               = "netDialerTimeout"
	clientTimeoutKey                  = "clientTimeout"
	reqTimeoutKey                     = "respWaitTimeout"
	reqRetryIntervalKey               = "requestRetryInterval"
	reqMaxRetriesKey                  = "requestMaxRetries"
	wrpSourceKey                      = "WRPSource"
	hooksSchemeKey                    = "hooksScheme"
	reducedTransactionLoggingCodesKey = "logging.reducedLoggingResponseCodes"
	authAcquirerKey                   = "authAcquirer"
	webhookConfigKey                  = "webhook"
	anclaClientConfigKey              = "webhook.BasicClientConfig"
)

var defaults = map[string]interface{}{
	translationServicesKey: []string{}, // no services allowed by the default
	targetURLKey:           "localhost:6000",
	netDialerTimeoutKey:    "5s",
	clientTimeoutKey:       "50s",
	reqTimeoutKey:          "40s",
	reqRetryIntervalKey:    "2s",
	reqMaxRetriesKey:       2,
	wrpSourceKey:           "dns:localhost",
	hooksSchemeKey:         "https",
}

func setupFlagSet(fs *pflag.FlagSet) {
	fs.StringP("file", "f", "", "the configuration file to use.  Overrides the search path.")
	fs.BoolP("debug", "d", false, "enables debug logging.  Overrides configuration.")
	fs.BoolP("version", "v", false, "print version and exit")
}

func Setup(args []string) (*viper.Viper, *zap.Logger, *pflag.FlagSet, error) {
	fs := pflag.NewFlagSet(ApplicationName, pflag.ContinueOnError)
	setupFlagSet(fs)
	err := fs.Parse(args)
	if err != nil {
		return nil, nil, fs, fmt.Errorf("failed to create parse args: %w", err)
	}

	v := viper.New()
	for k, va := range defaults {
		v.SetDefault(k, va)
	}

	if file, _ := fs.GetString("file"); len(file) > 0 {
		v.SetConfigFile(file)
		err = v.ReadInConfig()
	} else {
		v.SetConfigName(ApplicationName)
		v.AddConfigPath(fmt.Sprintf("/etc/%s", ApplicationName))
		v.AddConfigPath(fmt.Sprintf("$HOME/.%s", ApplicationName))
		v.AddConfigPath(".")
		err = v.ReadInConfig()
	}
	if err != nil {
		return v, nil, fs, fmt.Errorf("failed to read config file: %w", err)
	}

	if debug, _ := fs.GetBool("debug"); debug {
		v.Set("log.level", "DEBUG")
	}

	var c sallust.Config
	v.UnmarshalKey("logging", &c)
	l := zap.Must(c.Build())
	return v, l, fs, err
}
