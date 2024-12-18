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

	"github.com/xmidt-org/ancla/anclafx"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/arrange/arrangepprof"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	tr1d1um "github.com/xmidt-org/tr1d1um/internal"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/goph/emperror"
	"github.com/spf13/pflag"
	"github.com/xmidt-org/candlelight"
)

const (
	tracingConfigKey = "tracing"
)

var (
	// dynamic versioning
	Version string
	Date    string
	Commit  string
)

type XmidtClientTimeoutConfigIn struct {
	fx.In
	XmidtClientTimeout tr1d1um.HttpClientTimeout `name:"xmidtClientTimeout"`
}

type ArgusClientTimeoutConfigIn struct {
	fx.In
	ArgusClientTimeout tr1d1um.HttpClientTimeout `name:"argusClientTimeout"`
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
		DefaultKeyID: tr1d1um.DefaultKeyID,
	}
}

func configureXmidtClientTimeout(in XmidtClientTimeoutConfigIn) tr1d1um.HttpClientTimeout {
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

func configureArgusClientTimeout(in ArgusClientTimeoutConfigIn) tr1d1um.HttpClientTimeout {
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
	traceConfig.ApplicationName = tr1d1um.ApplicationName
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
	fmt.Fprintf(writer, "%s:\n", tr1d1um.ApplicationName)
	fmt.Fprintf(writer, "  version: \t%s\n", Version)
	fmt.Fprintf(writer, "  go version: \t%s\n", runtime.Version())
	fmt.Fprintf(writer, "  built time: \t%s\n", Date)
	fmt.Fprintf(writer, "  git commit: \t%s\n", Commit)
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
func Tr1d1um(arguments []string) (exitCode int) {
	v, l, f, err := tr1d1um.Setup(arguments)
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

	l = l.With(zap.Time("ts", time.Now().UTC()), zap.Any("caller", zap.WithCaller(true)))
	app := fx.New(
		arrange.LoggerFunc(l.Sugar().Infof),
		fx.Supply(l),
		fx.Supply(v),
		arrange.ForViper(v),
		arrange.ProvideKey("xmidtClientTimeout", tr1d1um.HttpClientTimeout{}),
		arrange.ProvideKey("argusClientTimeout", tr1d1um.HttpClientTimeout{}),
		touchstone.Provide(),
		touchhttp.Provide(),
		anclafx.Provide(),
		arrangepprof.HTTP{
			RouterName: "server_pprof",
		}.Provide(),
		fx.Provide(
			consts,
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
			tr1d1um.ProvideAnclaHTTPClient,
		),
		tr1d1um.ProvideAuthChain("authx.inbound"),
		tr1d1um.ProvideServers(),
		tr1d1um.ProvideHandlers(),
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
	os.Exit(Tr1d1um(os.Args))
}
