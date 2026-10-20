// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	"io"
	_ "net/http/pprof"
	"os"
	"runtime"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/tr1d1um/internal/viperfx"
	"github.com/xmidt-org/tr1d1um/webhook"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"

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
	webhookConfigKey                  = "webhook"
	tracingConfigKey                  = "tracing"
	fingerprintCredsKey               = "fingerprintCreds"
	parameterLoggingKey               = "parameterLogging"
)

var (
	// dynamic versioning
	Version string
	Date    string
	Commit  string
)

var defaults = map[string]any{
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

// printVersion writes the version to w and reports true when the flags ask
// for it.  It runs before the configuration is read, so the version can be
// printed without one.
func printVersion(fs *pflag.FlagSet, w io.Writer) bool {
	if pVersion, _ := fs.GetBool("version"); pVersion {
		printVersionInfo(w)
		return true
	}

	return false
}

func printVersionInfo(writer io.Writer) {
	fmt.Fprintf(writer, "%s:\n", applicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", Date)
	fmt.Fprintf(writer, "  git commit: \t%s\n", Commit)
	fmt.Fprintf(writer, "  os/arch: \t%s/%s\n", runtime.GOOS, runtime.GOARCH)
}

// newApp assembles the tr1d1um application from its configuration.
func newApp(v *viper.Viper, l *zap.Logger) fx.Option {
	return fx.Options(
		fx.WithLogger(func() fxevent.Logger {
			return &fxevent.ZapLogger{Logger: l}
		}),
		fx.Supply(l),
		fx.Supply(v),
		viperfx.Provide("xmidtClientTimeout", httpClientTimeout{}),
		auth.Provide(v),
		webhook.Provide(),
		touchstone.Provide(),
		touchhttp.Provide(),
		provideMetrics(),
		fx.Provide(
			consts,
			viperfx.Unmarshal(tracingConfigKey, candlelight.Config{}),
			fx.Annotated{
				Name:   "xmidt_client_timeout",
				Target: configureXmidtClientTimeout,
			},
			loadTracing,
			newHTTPClient,
		),
		provideServers(),
		provideHandlers(),
	)
}

func tr1d1um(arguments []string) (exitCode int) {
	fs, err := parseFlags(arguments)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	// This allows us to communicate the version of the binary upon request.
	if printVersion(fs, os.Stdout) {
		return 0
	}

	v, l, err := setup(fs)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	app := fx.New(newApp(v, l))

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
