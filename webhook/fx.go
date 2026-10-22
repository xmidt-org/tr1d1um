// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"go.uber.org/fx"

	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/anclafx"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/tr1d1um/internal/viperfx"
)

const module = "tr1d1um.webhook"

// defaultConfig is the webhook configuration before the file is read: every
// validation of a registration is on.  A deployment turns one off by
// setting it to false under webhook.validation.buildOpts; leaving the
// section out never turns them all off.
func defaultConfig() ancla.Config {
	return ancla.Config{
		Validation: schema.SchemaURLValidatorConfig{
			BuildOpts: schema.BuildOptions{
				AtLeastOneEvent:                true,
				EventRegexMustCompile:          true,
				DeviceIDRegexMustCompile:       true,
				ValidateRegistrationDuration:   true,
				ProvideReceiverURLValidator:    true,
				ProvideFailureURLValidator:     true,
				ProvideAlternativeURLValidator: true,
				CheckUntil:                     true,
			},
		},
	}
}

func Provide() fx.Option {
	return fx.Module(
		module,
		anclafx.Provide(),
		fx.Provide(
			viperfx.Unmarshal("webhook", defaultConfig()),
			viperfx.Unmarshal("webhook.client", clientConfig{}),
			viperfx.Unmarshal("webhook.listener", listnerConfig{}),
			fx.Annotate(NewClientOpts, fx.ResultTags(`group:"client_options,flatten"`)),
			fx.Annotate(NewListenerOpts, fx.ResultTags(`group:"listener_options,flatten"`, ``)),
		),
	)
}
