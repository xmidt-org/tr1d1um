/**
 * Copyright 2022 Comcast Cable Communications Management, LLC
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
	"errors"
	"fmt"
	"io"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/sallust/sallustkit"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/go-kit/kit/log"
	"github.com/go-kit/kit/log/level"
	"github.com/goph/emperror"
	"github.com/spf13/pflag"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

// convenient global values
const (
	DefaultKeyID             = "current"
	applicationName, apiBase = "tr1d1um", "api/v3"
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
	reducedTransactionLoggingCodesKey = "log.reducedLoggingResponseCodes"
	authAcquirerKey                   = "authAcquirer"
	webhookConfigKey                  = "webhook"
	tracingConfigKey                  = "tracing"
)

var (
	// dynamic versioning
	Version   string
	BuildTime string
	GitCommit string
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

//nolint:funlen
func tr1d1um(arguments []string) (exitCode int) {

	v, l, f, err := setup(arguments)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	logger := gokitLogger(l)

	// This allows us to communicate the version of the binary upon request.
	if done, parseErr := printVersion(f, arguments); done {
		// if we're done, we're exiting no matter what
		exitIfError(logger, emperror.Wrap(parseErr, "failed to parse arguments"))
		os.Exit(0)
	}

	app := fx.New(
		arrange.ForViper(v),
		arrange.LoggerFunc(l.Sugar().Infof),
		fx.Supply(logger),
		fx.Provide(
			gokitLogger,
			arrange.ProvideKey("xmidtClientTimeout", httpClientTimeout{}),
			arrange.ProvideKey("argusClientTimeout", httpClientTimeout{}),
			arrange.UnmarshalKey("tracingConfigKey", candlelight.Config{}),
			arrange.UnmarshalKey("authAcquirerKey", authAcquirerConfig{}),
			arrange.UnmarshalKey("webhookConfigKey", ancla.Config{}),

			fx.Annotated{
				Name:   "xmidt_client_timeout",
				Target: newXmidtClientTimeout,
			},
			fx.Annotated{
				Name:   "argus_client_timeout",
				Target: newArgusClientTimeout,
			},
			loadTracing,
			newHTTPClient,
		),
		provideServers(),
		provideHandlers(),
	)

	switch err := app.Err(); {
	case errors.Is(err, pflag.ErrHelp):
		return
	case err == nil:
		app.Run()
	default:
		fmt.Fprintln(os.Stderr, err)
		return 2
	}

	return 0
}

type XmidtClientTimeoutConfigIn struct {
	fx.In
	XmidtClientTimeout httpClientTimeout
}

func newXmidtClientTimeout(in XmidtClientTimeoutConfigIn) httpClientTimeout {
	xct := in.XmidtClientTimeout

	if xct.ClientTimeout == 0 {
		xct.ClientTimeout = time.Second * 135
	}
	if xct.NetDialerTimeout == 0 {
		xct.NetDialerTimeout = time.Second * 5
	}
	if xct.RequestTimeout == 0 {
		xct.RequestTimeout = time.Second * 129
	}
	return xct
}

type ArgusClientTimeoutConfigIn struct {
	fx.In
	ArgusClientTimeout httpClientTimeout
}

func newArgusClientTimeout(in ArgusClientTimeoutConfigIn) httpClientTimeout {
	act := in.ArgusClientTimeout

	if act.ClientTimeout == 0 {
		act.ClientTimeout = time.Second * 50
	}
	if act.NetDialerTimeout == 0 {
		act.NetDialerTimeout = time.Second * 5
	}
	return act
}

type TracingConfigIn struct {
	fx.In
	TracingConfig candlelight.Config
	Logger        log.Logger
}

func loadTracing(in TracingConfigIn) (candlelight.Tracing, error) {
	traceConfig := in.TracingConfig
	traceConfig.ApplicationName = applicationName
	tracing, err := candlelight.New(traceConfig)
	if err != nil {
		return candlelight.Tracing{}, err
	}
	level.Info(in.Logger).Log(logging.MessageKey(), "tracing status", "enabled", !tracing.IsNoop())
	return tracing, nil
}

func printVersion(f *pflag.FlagSet, arguments []string) (bool, error) {
	printVer := f.BoolP("version", "v", false, "displays the version number")
	if err := f.Parse(arguments); err != nil {
		return true, err
	}

	if *printVer {
		printVersionInfo(os.Stdout)
		return true, nil
	}
	return false, nil
}

func exitIfError(logger log.Logger, err error) {
	if err != nil {
		if logger != nil {
			logging.Error(logger, emperror.Context(err)...).Log(logging.ErrorKey(), err.Error())
		}
		fmt.Fprintf(os.Stderr, "Error: %#v\n", err.Error())
		os.Exit(1)
	}
}

func printVersionInfo(writer io.Writer) {
	fmt.Fprintf(writer, "%s:\n", applicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", BuildTime)
	fmt.Fprintf(writer, "  git commit: \t%s\n", GitCommit)
	fmt.Fprintf(writer, "  os/arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func gokitLogger(l *zap.Logger) log.Logger {
	return sallustkit.Logger{
		Zap: l,
	}
}

func main() {
	os.Exit(tr1d1um(os.Args))
}
