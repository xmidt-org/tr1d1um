// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"go.uber.org/fx"

	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/anclafx"
	"github.com/xmidt-org/tr1d1um/internal/viperfx"
)

const module = "tr1d1um.webhook"

func Provide() fx.Option {
	return fx.Module(
		module,
		anclafx.Provide(),
		fx.Provide(
			viperfx.Unmarshal("webhook", ancla.Config{}),
			viperfx.Unmarshal("webhook.client", clientConfig{}),
			viperfx.Unmarshal("webhook.listener", listnerConfig{}),
			fx.Annotate(NewClientOpts, fx.ResultTags(`group:"client_options,flatten"`)),
			fx.Annotate(NewListenerOpts, fx.ResultTags(`group:"listener_options,flatten"`, ``)),
		),
	)
}
