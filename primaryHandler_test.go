// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/tr1d1um/transaction"
	webhook "github.com/xmidt-org/webhook-schema"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

type noopLifecycle struct{}

func (noopLifecycle) Append(fx.Hook) {}

func TestCreateAuthAcquirer(t *testing.T) {
	tcs := []struct {
		name        string
		config      authAcquirerConfig
		expectType  string
		expectError bool
	}{
		{
			name: "returns JWT acquirer when full JWT config is set",
			config: authAcquirerConfig{
				JWT: transaction.RemoteBearerTokenAcquirerOptions{
					AuthURL: "https://auth.example/token",
					Timeout: 3 * time.Second,
					Buffer:  2 * time.Second,
				},
			},
			expectType: "jwt",
		},
		{
			name: "falls back to basic acquirer when JWT config is incomplete",
			config: authAcquirerConfig{
				JWT: transaction.RemoteBearerTokenAcquirerOptions{
					AuthURL: "https://auth.example/token",
					Timeout: 3 * time.Second,
				},
				Basic: "Basic dXNlcjpwYXNz",
			},
			expectType: "basic",
		},
		{
			name:        "returns error when no supported auth config is provided",
			config:      authAcquirerConfig{},
			expectError: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			acquirer, err := createAuthAcquirer(tc.config)
			if tc.expectError {
				require.Error(t, err)
				assert.Nil(t, acquirer)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, acquirer)

			switch tc.expectType {
			case "jwt":
				jwtAcquirer, ok := acquirer.(*transaction.JwtAcquirer)
				require.True(t, ok)
				assert.Equal(t, tc.config.JWT, jwtAcquirer.Config)
			case "basic":
				basicAcquirer, ok := acquirer.(*transaction.BasicAcquirer)
				require.True(t, ok)
				assert.Equal(t, tc.config.Basic, basicAcquirer.Token)
			default:
				t.Fatalf("unexpected expected type: %s", tc.expectType)
			}
		})
	}
}

func TestBuildWebhookValidators(t *testing.T) {
	tcs := []struct {
		name             string
		validationConfig schema.SchemaURLValidatorConfig
		registration     webhook.RegistrationV1
		expectBuildError bool
		expectValidError bool
	}{
		{
			name: "fails when validation has invalid forbidden subnet",
			validationConfig: schema.SchemaURLValidatorConfig{
				IP: schema.IPVConfig{ForbiddenSubnets: []string{"not-a-cidr"}},
			},
			expectBuildError: true,
		},
		{
			name: "rejects loopback URL when loopback is forbidden",
			validationConfig: schema.SchemaURLValidatorConfig{
				BuildOpts: schema.BuildOptions{ProvideReceiverURLValidator: true},
				URL:       schema.URLVConfig{AllowLoopback: false},
				IP:        schema.IPVConfig{Allow: true},
				Domain:    schema.DomainVConfig{AllowSpecialUseDomains: true},
			},
			registration: webhook.RegistrationV1{
				Config: webhook.DeliveryConfig{ReceiverURL: "http://127.0.0.1/callback"},
			},
			expectValidError: true,
		},
		{
			name: "accepts regular non-loopback URL",
			validationConfig: schema.SchemaURLValidatorConfig{
				BuildOpts: schema.BuildOptions{ProvideReceiverURLValidator: true},
				URL:       schema.URLVConfig{AllowLoopback: false},
				IP:        schema.IPVConfig{Allow: true},
				Domain:    schema.DomainVConfig{AllowSpecialUseDomains: true},
			},
			registration: webhook.RegistrationV1{
				Config: webhook.DeliveryConfig{ReceiverURL: "https://example.com/callback"},
			},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			validators, err := buildWebhookValidators(tc.validationConfig)
			if tc.expectBuildError {
				require.Error(t, err)
				assert.Nil(t, validators)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, validators)

			validationErr := validators.Validate(&tc.registration)
			if tc.expectValidError {
				assert.Error(t, validationErr)
				return
			}
			assert.NoError(t, validationErr)
		})
	}
}

func TestV2WebhookValidators(t *testing.T) {
	tcs := []struct {
		name             string
		config           ancla.Config
		registration     webhook.RegistrationV1
		expectBuildError bool
		expectValidError bool
	}{
		{
			name: "allows loopback and special-use domains for v2 compatibility",
			config: ancla.Config{
				Validation: schema.SchemaURLValidatorConfig{
					BuildOpts: schema.BuildOptions{ProvideReceiverURLValidator: true},
					URL:       schema.URLVConfig{AllowLoopback: false},
					IP:        schema.IPVConfig{Allow: false},
					Domain:    schema.DomainVConfig{AllowSpecialUseDomains: false},
				},
			},
			registration: webhook.RegistrationV1{
				Config: webhook.DeliveryConfig{ReceiverURL: "http://localhost:8080/callback"},
			},
		},
		{
			name: "returns error when underlying checker config is invalid",
			config: ancla.Config{
				Validation: schema.SchemaURLValidatorConfig{
					BuildOpts: schema.BuildOptions{ProvideReceiverURLValidator: true},
					IP:        schema.IPVConfig{ForbiddenSubnets: []string{"bad-cidr"}},
				},
			},
			expectBuildError: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			validators, err := v2WebhookValidators(tc.config)
			if tc.expectBuildError {
				require.Error(t, err)
				assert.Nil(t, validators)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, validators)

			validationErr := validators.Validate(&tc.registration)
			if tc.expectValidError {
				assert.Error(t, validationErr)
				return
			}
			assert.NoError(t, validationErr)
		})
	}
}

func TestProvideWebhookHandlers(t *testing.T) {
	tcs := []struct {
		name               string
		setWebhookConfig   bool
		webhookConfig      ancla.Config
		expectError        bool
		expectHandlersMade bool
	}{
		{
			name:               "returns with no handlers when webhook config key is not set",
			setWebhookConfig:   false,
			webhookConfig:      ancla.Config{},
			expectHandlersMade: false,
		},
		{
			name:             "returns handlers when webhook config key is set and validation is valid",
			setWebhookConfig: true,
			webhookConfig: ancla.Config{
				Validation: schema.SchemaURLValidatorConfig{},
			},
			expectHandlersMade: true,
		},
		{
			name:             "returns error when validation checker cannot be built",
			setWebhookConfig: true,
			webhookConfig: ancla.Config{
				Validation: schema.SchemaURLValidatorConfig{
					IP: schema.IPVConfig{ForbiddenSubnets: []string{"invalid-cidr"}},
				},
			},
			expectError: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			if tc.setWebhookConfig {
				v.Set(webhookConfigKey, true)
			}

			out, err := provideWebhookHandlers(provideWebhookHandlersIn{
				Lifecycle:     noopLifecycle{},
				V:             v,
				WebhookConfig: tc.webhookConfig,
				Logger:        zap.NewNop(),
			})

			if tc.expectError {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			if tc.expectHandlersMade {
				assert.NotNil(t, out.AddWebhookHandler)
				assert.NotNil(t, out.V2AddWebhookHandler)
				assert.NotNil(t, out.GetAllWebhooksHandler)
				return
			}

			assert.Nil(t, out.AddWebhookHandler)
			assert.Nil(t, out.V2AddWebhookHandler)
			assert.Nil(t, out.GetAllWebhooksHandler)
		})
	}
}

func TestSimpleWebhookService(t *testing.T) {
	tcs := []struct {
		name      string
		manifests []schema.Manifest
		expected  int
	}{
		{
			name:      "empty store returns empty list",
			manifests: nil,
			expected:  0,
		},
		{
			name: "add stores manifests and getall returns them",
			manifests: []schema.Manifest{
				&schema.ManifestV1{Registration: webhook.RegistrationV1{Config: webhook.DeliveryConfig{ReceiverURL: "https://one.example/callback"}}},
				&schema.ManifestV1{Registration: webhook.RegistrationV1{Config: webhook.DeliveryConfig{ReceiverURL: "https://two.example/callback"}}},
			},
			expected: 2,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			svc := &simpleWebhookService{logger: zap.NewNop()}

			for _, manifest := range tc.manifests {
				err := svc.Add(context.Background(), "owner", manifest)
				require.NoError(t, err)
			}

			got, err := svc.GetAll(context.Background())
			require.NoError(t, err)
			assert.Len(t, got, tc.expected)
		})
	}
}
