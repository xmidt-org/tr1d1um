// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"errors"
	"fmt"
	_ "net/http/pprof"
	"os"
	"time"

	"github.com/goschtalt/goschtalt"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/arrange/arrangehttp"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/spf13/pflag"
	"github.com/xmidt-org/candlelight"

	"github.com/alecthomas/kong"
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
	translationServicesKey = "supportedServices"
	targetURLKey           = "targetURL"
	netDialerTimeoutKey    = "netDialerTimeout"
	clientTimeoutKey       = "clientTimeout"
	reqTimeoutKey          = "respWaitTimeout"
	reqRetryIntervalKey    = "requestRetryInterval"
	reqMaxRetriesKey       = "requestMaxRetries"
	wrpSourceKey           = "WRPSource"
	hooksSchemeKey         = "hooksScheme"
	authAcquirerKey        = "authAcquirer"
	webhookConfigKey       = "webhook"
)

var (
	commit  = "undefined"
	version = "undefined"
	date    = "undefined"
	builtBy = "undefined"
)

type CLI struct {
	Dev   bool     `optional:"" short:"d" help:"Run in development mode."`
	Show  bool     `optional:"" short:"s" help:"Show the configuration and exit."`
	Graph string   `optional:"" short:"g" help:"Output the dependency graph to the specified file."`
	Files []string `optional:"" short:"f" help:"Specific configuration files or directories."`
}

// Provides a named type so it's a bit easier to flow through & use in fx.
type cliArgs []string

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
	var (
		gscfg *goschtalt.Config

		// Capture the dependency tree in case we need to debug something.
		g fx.DotGraph

		// Capture the command line arguments.
		cli *CLI
	)

	app := fx.New(
		fx.Supply(cliArgs(arguments)),
		fx.Populate(&g),
		fx.Populate(&gscfg),
		fx.Populate(&cli),

		fx.WithLogger(func(log *zap.Logger) fxevent.Logger {
			return &fxevent.ZapLogger{Logger: log}
		}),

		fx.Provide(
			provideCLI,
			provideLogger,
			provideConfig,
			provideWebhookHandlers,
			goschtalt.UnmarshalFunc[sallust.Config]("logging"),
			goschtalt.UnmarshalFunc[candlelight.Config]("tracing"),
			goschtalt.UnmarshalFunc[touchstone.Config]("prometheus"),
			goschtalt.UnmarshalFunc[JWTValidator]("jwtValidator"),
			goschtalt.UnmarshalFunc[[]int]("logging.reducedLoggingResponseCodes"),
			goschtalt.UnmarshalFunc[bool]("previousVersionSupport"),
			goschtalt.UnmarshalFunc[int]("requestMaxRetries"),
			goschtalt.UnmarshalFunc[time.Duration]("requestRetryInterval"),
			goschtalt.UnmarshalFunc[[]string]("supportedServices"),
			consts,
			fx.Annotated{
				Name:   "WRPSource",
				Target: goschtalt.UnmarshalFunc[string]("wrpSource"),
			},
			fx.Annotated{
				Name:   "targetURL",
				Target: goschtalt.UnmarshalFunc[string]("targetUrl"),
			},
			fx.Annotated{
				Name:   "argusClientTimeout",
				Target: goschtalt.UnmarshalFunc[httpClientTimeout]("argusClientTimeout"),
			},
			fx.Annotated{
				Name:   "xmidtClientTimeout",
				Target: goschtalt.UnmarshalFunc[httpClientTimeout]("xmidtClientTimeout"),
			},
			fx.Annotated{
				Name:   "xmidt_client_timeout",
				Target: configureXmidtClientTimeout,
			},
			fx.Annotated{
				Name:   "argus_client_timeout",
				Target: configureArgusClientTimeout,
			},
			fx.Annotated{
				Name:   "server",
				Target: goschtalt.UnmarshalFunc[string]("server"),
			},
			fx.Annotated{
				Name:   "build",
				Target: goschtalt.UnmarshalFunc[string]("build"),
			},
			fx.Annotated{
				Name:   "flavor",
				Target: goschtalt.UnmarshalFunc[string]("flavor"),
			},
			fx.Annotated{
				Name:   "region",
				Target: goschtalt.UnmarshalFunc[string]("region"),
			},
			fx.Annotated{
				Name:   "servers.health.config",
				Target: goschtalt.UnmarshalFunc[arrangehttp.ServerConfig]("servers.health.http"),
			},
			fx.Annotated{
				Name:   "servers.metrics.config",
				Target: goschtalt.UnmarshalFunc[arrangehttp.ServerConfig]("servers.metrics.http"),
			},
			fx.Annotated{
				Name:   "servers.pprof.config",
				Target: goschtalt.UnmarshalFunc[arrangehttp.ServerConfig]("servers.pprof.http"),
			},
			fx.Annotated{
				Name:   "servers.primary.config",
				Target: goschtalt.UnmarshalFunc[arrangehttp.ServerConfig]("servers.primary.http"),
			},
			fx.Annotated{
				Name:   "servers.alternate.config",
				Target: goschtalt.UnmarshalFunc[arrangehttp.ServerConfig]("servers.alternate.http"),
			},
			candlelight.New,
			newHTTPClient,
		),

		arrangehttp.ProvideServer("servers.health"),
		arrangehttp.ProvideServer("servers.metrics"),
		arrangehttp.ProvideServer("servers.pprof"),
		arrangehttp.ProvideServer("servers.primary"),
		arrangehttp.ProvideServer("servers.alternate"),

		touchstone.Provide(),
		touchhttp.Provide(),
		ancla.ProvideMetrics(),
		provideAuthChain("authx.inbound"),
		provideServerOptions(),
		provideServerMetrics(),
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

func provideCLI(args cliArgs) (*CLI, error) {
	return provideCLIWithOpts(args, false)
}

func provideCLIWithOpts(args cliArgs, testOpts bool) (*CLI, error) {
	var cli CLI

	// Create a no-op option to satisfy the kong.New() call.
	var opt kong.Option = kong.OptionFunc(
		func(*kong.Kong) error {
			return nil
		},
	)

	if testOpts {
		opt = kong.Writers(nil, nil)
	}

	parser, err := kong.New(&cli,
		kong.Name(applicationName),
		kong.Description("The cpe agent for Xmidt service.\n"+
			fmt.Sprintf("\tVersion:  %s\n", version)+
			fmt.Sprintf("\tDate:     %s\n", date)+
			fmt.Sprintf("\tCommit:   %s\n", commit)+
			fmt.Sprintf("\tBuilt By: %s\n", builtBy),
		),
		kong.UsageOnError(),
		opt,
	)
	if err != nil {
		return nil, err
	}

	if testOpts {
		parser.Exit = func(_ int) { panic("exit") }
	}

	_, err = parser.Parse(args)
	if err != nil {
		parser.FatalIfErrorf(err)
	}

	return &cli, nil
}
