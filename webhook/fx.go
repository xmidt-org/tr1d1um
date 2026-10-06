// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/anclafx"
	"github.com/xmidt-org/arrange"
	"go.uber.org/fx"
)

const module = "tr1d1um.webhook"

func Provide() fx.Option {
	return fx.Module(
		module,
		anclafx.Provide(),
		fx.Provide(
			arrange.UnmarshalKey(webhookConfigKey, ancla.Config{}),
			arrange.UnmarshalKey(clientConfigKey, clientConfig{}),
			arrange.UnmarshalKey(listenerConfigKey, listnerConfig{}),
			fx.Annotate(NewClientOpts, fx.ResultTags(`group:"client_options,flatten"`)),
			fx.Annotate(NewListenerOpts, fx.ResultTags(`group:"listener_options,flatten"`, ``)),
		),
	)
}
