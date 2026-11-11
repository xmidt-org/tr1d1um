// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/tr1d1um/internal/viperfx"
	"go.uber.org/fx"
)

func TestNewOutputFormat(t *testing.T) {
	for in, want := range map[string]schema.OutputFormat{"": schema.OutputLegacy, "legacy": schema.OutputLegacy, "v1": schema.OutputV1} {
		got, err := NewOutputFormat(Config{OutputFormat: in})
		require.NoError(t, err, in)
		assert.Equal(t, want, got, in)
	}

	_, err := NewOutputFormat(Config{OutputFormat: "v9"})
	assert.Error(t, err)
}

func newTestViper(t *testing.T, yaml string) *viper.Viper {
	t.Helper()
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))

	return v
}

// TestConfigFromViper checks the keys in the configuration file reach the
// components the handlers and the registry are built from.
func TestConfigFromViper(t *testing.T) {
	tests := []struct {
		desc       string
		yaml       string
		wantConfig Config
		wantFormat schema.OutputFormat
		wantErr    bool
	}{
		{desc: "defaults", yaml: "webhook: {}\n", wantFormat: schema.OutputLegacy},
		{
			desc:       "both set",
			yaml:       "webhook:\n  outputFormat: v1\n  filterByOwner: true\n",
			wantConfig: Config{OutputFormat: "v1", FilterByOwner: true},
			wantFormat: schema.OutputV1,
		},
		{desc: "bad format", yaml: "webhook:\n  outputFormat: v9\n", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			var (
				cfg    Config
				format schema.OutputFormat
				anc    ancla.Config
			)
			app := fx.New(
				fx.NopLogger,
				fx.Provide(
					func() *viper.Viper { return newTestViper(t, tc.yaml) },
					viperfx.Unmarshal("webhook", defaultConfig()),
					viperfx.Unmarshal("webhook", Config{}),
					NewOutputFormat,
				),
				fx.Populate(&cfg, &format, &anc),
			)
			if tc.wantErr {
				require.Error(t, app.Err())
				return
			}
			require.NoError(t, app.Err())
			assert.Equal(t, tc.wantConfig, cfg)
			assert.Equal(t, tc.wantFormat, format)
			assert.True(t, anc.Validation.BuildOpts.AtLeastOneEvent, "validation defaults are kept")
		})
	}
}

func TestDefaultConfigValidationOn(t *testing.T) {
	b := defaultConfig().Validation.BuildOpts
	assert.True(t, b.AtLeastOneEvent && b.EventRegexMustCompile && b.DeviceIDRegexMustCompile &&
		b.ValidateRegistrationDuration && b.ProvideReceiverURLValidator &&
		b.ProvideFailureURLValidator && b.ProvideAlternativeURLValidator && b.CheckUntil)
}
