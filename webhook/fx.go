// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/anclafx"
	"github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/ancla/chrysom"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/candlelight"
	"go.uber.org/fx"
)

const module = "tr1d1um.webhook"

func Provide() fx.Option {
	return fx.Module(
		module,
		anclafx.Provide(),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
			arrange.UnmarshalKey(clientConfigKey, config{}),
			provideClientOpts,
		),
	)
}

type clientIn struct {
	fx.In

	Cfg     config
	Tracing candlelight.Tracing
	Auth    auth.Decorator
}

type clientOut struct {
	fx.Out

	ClientOpts chrysom.ClientOptions `group:"client_options,flatten"`
}

func provideClientOpts(in clientIn) clientOut {
	return clientOut{ClientOpts: NewClientOpts(in.Cfg, in.Tracing, in.Auth)}
}
