// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"fmt"

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

// Config holds the webhook settings tr1d1um adds to ancla's own.
type Config struct {
	// OutputFormat is the layout a v1 registration is written in: "legacy"
	// (the default) is readable by every release; "v1" is not readable by
	// ancla v0.3.12 readers, so upgrade them (caduceus) first.
	OutputFormat string

	// FilterByOwner, if true, lists to a caller only the webhooks it
	// registered itself, rather than every webhook its partners may see.
	FilterByOwner bool
}

// NewOutputFormat parses the configured output format.
func NewOutputFormat(c Config) (schema.OutputFormat, error) {
	var f schema.OutputFormat
	if err := f.UnmarshalText([]byte(c.OutputFormat)); err != nil {
		return f, fmt.Errorf("invalid webhook.outputFormat: %w", err)
	}

	return f, nil
}

func Provide() fx.Option {
	return fx.Module(
		module,
		anclafx.Provide(),
		fx.Provide(
			viperfx.Unmarshal("webhook", defaultConfig()),
			viperfx.Unmarshal("webhook", Config{}),
			NewOutputFormat,
			viperfx.Unmarshal("webhook.client", clientConfig{}),
			viperfx.Unmarshal("webhook.listener", listnerConfig{}),
			fx.Annotate(NewClientOpts, fx.ResultTags(`group:"client_options,flatten"`)),
			fx.Annotate(NewListenerOpts, fx.ResultTags(`group:"listener_options,flatten"`, ``)),
		),
	)
}
