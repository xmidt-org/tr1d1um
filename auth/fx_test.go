// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/clortho"
	"github.com/xmidt-org/clortho/clorthofx"
	"github.com/xmidt-org/touchstone"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// clortho's configuration types carry no struct tags, so this checks that the
// keys written in tr1d1um.yaml land on the right fields.
func TestClorthoConfigFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    jwt:
      clortho:
        client:
          clientTimeout: 7s
          netDialerTimeout: 2s
        keySets:
          - sources:
              - uri: "https://keys.example/jwks"
                refreshInterval: 1h
                minRefreshInterval: 5m
              - uri: "/etc/tr1d1um/keys.json"
            refreshOnUnknownKeyID: true
        perKeys:
          - template: "https://themis.example/keys/{keyID}"
            allowedKeyIDs: ["a", "b"]
            fetchRateLimit: 3s
        fixed:
          - keys:
              - keyID: "fixed"
                key: "unused here"
            verify:
              ignoreKeyUsage: true
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg clorthoConfig
	require.NoError(t, v.UnmarshalKey(clorthoConfigKey, &cfg))

	assert.Equal(t, 7*time.Second, cfg.Client.ClientTimeout)
	assert.Equal(t, 2*time.Second, cfg.Client.NetDialerTimeout)

	require.Len(t, cfg.KeySets, 1)
	require.Len(t, cfg.KeySets[0].Sources, 2)
	assert.Equal(t, "https://keys.example/jwks", cfg.KeySets[0].Sources[0].URI)
	assert.Equal(t, time.Hour, cfg.KeySets[0].Sources[0].RefreshInterval)
	assert.Equal(t, 5*time.Minute, cfg.KeySets[0].Sources[0].MinRefreshInterval)
	assert.Equal(t, "/etc/tr1d1um/keys.json", cfg.KeySets[0].Sources[1].URI)
	assert.True(t, cfg.KeySets[0].RefreshOnUnknownKeyID)

	require.Len(t, cfg.PerKeys, 1)
	assert.Equal(t, "https://themis.example/keys/{keyID}", cfg.PerKeys[0].Template)
	assert.Equal(t, []string{"a", "b"}, cfg.PerKeys[0].AllowedKeyIDs)
	assert.Equal(t, 3*time.Second, cfg.PerKeys[0].FetchRateLimit)

	require.Len(t, cfg.Fixed, 1)
	require.Len(t, cfg.Fixed[0].Keys, 1)
	assert.Equal(t, "fixed", cfg.Fixed[0].Keys[0].KeyID)
	assert.True(t, cfg.Fixed[0].Verify.IgnoreKeyUsage)

	out := newClorthoConfig(cfg)

	// http sources and per-key providers get the client; a file source must
	// not, since clortho rejects a file source that has one.
	https := out.KeySets[0].Sources[0].Client
	require.NotNil(t, https)
	assert.Equal(t, 7*time.Second, https.Timeout)
	assert.Nil(t, out.KeySets[0].Sources[1].Client)
	assert.Same(t, https, out.PerKeys[0].Client)

	_, err := clortho.NewKeySetProvider(out.KeySets[0])
	assert.NoError(t, err)
	_, err = clortho.NewPerKeyProvider(out.PerKeys[0])
	assert.NoError(t, err)
}

func TestNewClorthoHTTPClient(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		client := newClorthoHTTPClient(clorthoClientConfig{})
		assert.Equal(t, defaultClorthoClientTimeout, client.Timeout)
		assert.NotNil(t, client.Transport)
	})

	t.Run("does not follow redirects", func(t *testing.T) {
		client := newClorthoHTTPClient(clorthoClientConfig{ClientTimeout: time.Second})
		require.NotNil(t, client.CheckRedirect)
		assert.ErrorIs(t, client.CheckRedirect(nil, nil), http.ErrUseLastResponse)
	})
}

func TestIsHTTPSource(t *testing.T) {
	assert.True(t, isHTTPSource("http://keys.example/jwks"))
	assert.True(t, isHTTPSource("https://keys.example/jwks"))
	assert.False(t, isHTTPSource("file:///etc/keys.json"))
	assert.False(t, isHTTPSource("/etc/keys.json"))
	assert.False(t, isHTTPSource("keys.example/jwks"))
}

func TestProvideDecoratorsParseOpts(t *testing.T) {
	assert.Len(t, provideDecoratorsParseOpts(nil), 1)
	assert.Len(t, provideDecoratorsParseOpts(newTestKeys(t).provider), 1)
}

// TestProvide builds the real fx graph in each inbound mode.  It runs the
// constructors, so it catches a wiring mistake such as a missing optional tag
// or clortho refusing its configuration, without starting anything.
func TestProvide(t *testing.T) {
	keys := newTestKeys(t)

	der, err := x509.MarshalPKIXPublicKey(&keys.private.PublicKey)
	require.NoError(t, err)
	publicPEM := string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))

	credential := encodedCredential("user", "pass")
	outbound := map[string]any{
		fanoutBasicConfigKey:   credential,
		webhookBascicConfigKey: credential,
	}

	cases := []struct {
		name    string
		set     map[string]any
		wantErr error
	}{
		{
			name: "basic",
			set: map[string]any{
				basicKey: []string{credential},
			},
		}, {
			name: "jwt with a fixed key",
			set: map[string]any{
				jwtPrefixesKey: []string{testPrefix},
				clorthoConfigKey + ".Fixed": []map[string]any{{
					"keys": []map[string]any{{"keyID": testKeyID, "key": publicPEM}},
				}},
			},
		}, {
			name: "jwt with no key providers",
			set: map[string]any{
				jwtPrefixesKey: []string{testPrefix},
			},
			wantErr: clorthofx.ErrNoProviders,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			for k, val := range outbound {
				v.Set(k, val)
			}
			for k, val := range tc.set {
				v.Set(k, val)
			}

			app := fx.New(
				fx.NopLogger,
				fx.Supply(v, zap.NewNop(), touchstone.Config{}),
				arrange.ForViper(v),
				touchstone.Provide(),
				Provide(v),
				fx.Invoke(func(in struct {
					fx.In
					Chain   alice.Chain `name:"auth_chain"`
					Fanout  Decorator
					Webhook anclaauth.Decorator
				}) {
					assert.NotNil(t, in.Fanout)
					assert.NotNil(t, in.Webhook)
				}),
			)

			if tc.wantErr != nil {
				assert.ErrorIs(t, app.Err(), tc.wantErr)
			} else {
				assert.NoError(t, app.Err())
			}
		})
	}
}
