// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package viperfx

import (
	"strings"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
)

const testConfig = `
retries: 3
interval: 2s
enabled: false
services: ["config", "stat"]
client:
  timeout: 45s
  name: "argus"
  nested:
    codes: [200, 504]
broken:
  timeout: "not a duration"
`

type nested struct {
	Codes []int
}

type client struct {
	Timeout time.Duration
	Name    string
	Retries int
	Nested  nested
}

func testViper(t *testing.T) *viper.Viper {
	t.Helper()

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(testConfig)))

	return v
}

func TestUnmarshal(t *testing.T) {
	v := testViper(t)

	t.Run("struct", func(t *testing.T) {
		got, err := Unmarshal("client", client{})(v)
		require.NoError(t, err)
		assert.Equal(t, client{
			Timeout: 45 * time.Second,
			Name:    "argus",
			Nested:  nested{Codes: []int{200, 504}},
		}, got)
	})

	t.Run("defaults fill what the configuration leaves out", func(t *testing.T) {
		got, err := Unmarshal("client", client{Retries: 7, Name: "overridden"})(v)
		require.NoError(t, err)
		assert.Equal(t, 7, got.Retries)
		assert.Equal(t, "argus", got.Name)
	})

	t.Run("absent key yields the defaults", func(t *testing.T) {
		got, err := Unmarshal("nosuch", client{Name: "default"})(v)
		require.NoError(t, err)
		assert.Equal(t, client{Name: "default"}, got)
	})

	t.Run("scalars", func(t *testing.T) {
		retries, err := Unmarshal("retries", 0)(v)
		require.NoError(t, err)
		assert.Equal(t, 3, retries)

		interval, err := Unmarshal("interval", time.Duration(0))(v)
		require.NoError(t, err)
		assert.Equal(t, 2*time.Second, interval)

		enabled, err := Unmarshal("enabled", true)(v)
		require.NoError(t, err)
		assert.False(t, enabled)

		services, err := Unmarshal("services", []string{})(v)
		require.NoError(t, err)
		assert.Equal(t, []string{"config", "stat"}, services)
	})

	t.Run("absent scalar yields its default", func(t *testing.T) {
		enabled, err := Unmarshal("nosuch", true)(v)
		require.NoError(t, err)
		assert.True(t, enabled)
	})

	t.Run("viper defaults are honored", func(t *testing.T) {
		v := testViper(t)
		v.SetDefault("target", "localhost:6000")

		target, err := Unmarshal("target", "")(v)
		require.NoError(t, err)
		assert.Equal(t, "localhost:6000", target)
	})

	t.Run("a value of the wrong type is an error naming the key", func(t *testing.T) {
		_, err := Unmarshal("broken", client{})(v)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "broken")
	})
}

func TestProvide(t *testing.T) {
	v := testViper(t)

	var got struct {
		fx.In
		Retries int    `name:"retries"`
		Client  client `name:"client"`
	}

	app := fx.New(
		fx.NopLogger,
		fx.Supply(v),
		Provide("retries", 0),
		Provide("client", client{}),
		fx.Populate(&got),
	)

	require.NoError(t, app.Err())
	assert.Equal(t, 3, got.Retries)
	assert.Equal(t, "argus", got.Client.Name)
}
