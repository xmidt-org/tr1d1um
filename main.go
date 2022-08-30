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

	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/sallust/sallustkit"
	"github.com/xmidt-org/touchstone/touchhttp"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/go-kit/kit/log"
	"github.com/goph/emperror"
	"github.com/spf13/pflag"
	"github.com/xmidt-org/candlelight"
)

// convenient global values
const (
	DefaultKeyID       = "current"
	apiVersion         = "v3"
	prevAPIVersion     = "v2"
	applicationName    = "tr1d1um"
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

type XmidtClientTimeoutConfigIn struct {
	fx.In
	XmidtClientTimeout httpClientTimeout `name:"xmidtClientTimeout"`
}

type ArgusClientTimeoutConfigIn struct {
	fx.In
	ArgusClientTimeout httpClientTimeout `name:"argusClientTimeout"`
}

type TracingConfigIn struct {
	fx.In
	TracingConfig candlelight.Config
	Logger        *zap.Logger
}

type ConstOut struct {
	fx.Out
	DefaultKeyID string `name:"default_key_id"`
}

func consts() ConstOut {
	return ConstOut{
		DefaultKeyID: DefaultKeyID,
	}
}

func gokitLogger(l *zap.Logger) log.Logger {
	return sallustkit.Logger{
		Zap: l,
	}
}

func configureXmidtClientTimeout(in XmidtClientTimeoutConfigIn) httpClientTimeout {
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

func configureArgusClientTimeout(in ArgusClientTimeoutConfigIn) httpClientTimeout {
	act := in.ArgusClientTimeout

	if act.ClientTimeout == 0 {
		act.ClientTimeout = time.Second * 50
	}
	if act.NetDialerTimeout == 0 {
		act.NetDialerTimeout = time.Second * 5
	}
	return act
}

func loadTracing(in TracingConfigIn) (candlelight.Tracing, error) {
	traceConfig := in.TracingConfig
	traceConfig.ApplicationName = applicationName
	tracing, err := candlelight.New(traceConfig)
	if err != nil {
		return candlelight.Tracing{}, err
	}
	in.Logger.Info("tracing status", zap.Bool("enabled", !tracing.IsNoop()))
	return tracing, nil
}

func printVersion(f *pflag.FlagSet, arguments []string) (bool, error) {
	if err := f.Parse(arguments); err != nil {
		return true, err
	}

	if pVersion, _ := f.GetBool("version"); pVersion {
		printVersionInfo(os.Stdout)
		return true, nil
	}
	return false, nil
}

func printVersionInfo(writer io.Writer) {
	fmt.Fprintf(writer, "%s:\n", applicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", BuildTime)
	fmt.Fprintf(writer, "  git commit: \t%s\n", GitCommit)
	fmt.Fprintf(writer, "  os/arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
}

func exitIfError(logger *zap.Logger, err error) {
	if err != nil {
		if logger != nil {
			logger.Error("failed to parse arguments", zap.Error(err))
		}
		fmt.Fprintf(os.Stderr, "Error: %#v\n", err.Error())
		os.Exit(1)
	}
}

//nolint:funlen
func tr1d1um(arguments []string) (exitCode int) {
	v, l, f, err := setup(arguments)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// This allows us to communicate the version of the binary upon request.
	if done, parseErr := printVersion(f, arguments); done {
		// if we're done, we're exiting no matter what
		exitIfError(l, emperror.Wrap(parseErr, "failed to parse arguments"))
		os.Exit(0)
	}

	app := fx.New(
		arrange.LoggerFunc(l.Sugar().Infof),
		fx.Supply(l),
		fx.Supply(v),
		arrange.ForViper(v),
		arrange.ProvideKey("xmidtClientTimeout", httpClientTimeout{}),
		arrange.ProvideKey("argusClientTimeout", httpClientTimeout{}),
		touchhttp.Provide(),
		ancla.ProvideMetrics(),
		fx.Provide(
			consts,
			gokitLogger,
			arrange.UnmarshalKey(tracingConfigKey, candlelight.Config{}),
			fx.Annotated{
				Name:   "xmidt_client_timeout",
				Target: configureXmidtClientTimeout,
			},
			fx.Annotated{
				Name:   "argus_client_timeout",
				Target: configureArgusClientTimeout,
			},
			loadTracing,
			newHTTPClient,
		),
		provideAuthChain("authx.inbound"),
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

func main() {
	os.Exit(tr1d1um(os.Args))
}
