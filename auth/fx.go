// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"fmt"

	"github.com/justinas/alice"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/clortho"
	"github.com/xmidt-org/clortho/clorthofx"
	"github.com/xmidt-org/touchstone"
	"go.uber.org/fx"
	"go.uber.org/multierr"
	"go.uber.org/zap"
)

const module = "tr1d1um.auth"

func Provide() fx.Option {
	return fx.Module(
		module,
		clorthofx.Provide(),
		provideMetrics(),
		fx.Provide(
			arrange.UnmarshalKey(inboundConfigKey, inboundConfig{}),
			arrange.UnmarshalKey(clorthoConfigKey, clortho.Config{}),
			arrange.UnmarshalKey(outboundConfigKey, outboundConfig{}),
			fx.Annotate(provideDecoratorsParseOpts, fx.ResultTags(`group:"jwt_parse_options,flatten"`)),
			provideDecorators,
			provideMiddleware,
		),
	)
}

func provideMetrics() fx.Option {
	return touchstone.CounterVec(
		prometheus.CounterOpts{
			Name: AuthCapabilityCheckCount,
			Help: "Counter for the capability check, providing outcome information by client, partner, and endpoint",
		},
		OutcomeLabel,
		ReasonLabel,
		ClientIDLabel,
		PartnerIDLabel,
		EndpointLabel,
		MethodLabel,
	)
}

type chainIn struct {
	fx.In

	Cfg     inboundConfig
	V       *viper.Viper
	Keyring clortho.KeyRing
	Logger  *zap.Logger
	Counter *prometheus.CounterVec `name:"auth_capability_check"`
}

type chainOut struct {
	fx.Out

	Middleware alice.Chain `name:"auth_chain"`
}

func provideMiddleware(in chainIn) (chainOut, error) {
	middle, err := NewMiddleware(in.Cfg, in.V, in.Keyring, in.Logger, in.Counter)

	return chainOut{Middleware: middle}, err
}

type decoratorIn struct {
	fx.In

	Cfg       outboundConfig
	V         *viper.Viper
	PraseOpts []jwt.ParseOption `group:"jwt_parse_options"`
}

func provideDecorators(in decoratorIn) (Decorator, auth.Decorator, error) {
	fod, err := NewDecorator(in.Cfg.Fanout, in.V, fanoutJWTConfigKey, fanoutBasicConfigKey, in.PraseOpts...)
	wd, err1 := NewDecorator(in.Cfg.Webhook, in.V, webhookJWTConfigKey, webhookBascicConfigKey, in.PraseOpts...)

	return fod, wd, multierr.Append(err, err1)
}

func provideDecoratorsParseOpts(kr clortho.KeyRing) ([]jwt.ParseOption, error) {
	kp, err := clortho.NewKeyProvider(clortho.WithRingKey(kr))
	if err != nil {
		return nil, fmt.Errorf("error setting up clortho KeyProvider: %v", err)
	}

	return []jwt.ParseOption{jwt.WithKeyProvider(kp)}, nil
}
