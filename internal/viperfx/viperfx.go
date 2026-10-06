// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

// Package viperfx makes values from a viper configuration available as
// go.uber.org/fx components.
package viperfx

import (
	"fmt"

	"github.com/spf13/viper"
	"go.uber.org/fx"
)

// Unmarshal returns an fx constructor that unmarshals the configuration at key
// into a T.  The constructor depends on the application's *viper.Viper.
//
// The defaults value is the starting point: anything the configuration does
// not set keeps the value given there, and a key that is absent altogether
// yields defaults unchanged.
func Unmarshal[T any](key string, defaults T) func(*viper.Viper) (T, error) {
	return func(v *viper.Viper) (T, error) {
		value := defaults
		if err := v.UnmarshalKey(key, &value); err != nil {
			var zero T
			return zero, fmt.Errorf("unable to unmarshal the configuration key `%s`: %w", key, err)
		}

		return value, nil
	}
}

// Provide unmarshals the configuration at key, as Unmarshal does, and provides
// the result as a component named key.
func Provide[T any](key string, defaults T) fx.Option {
	return fx.Provide(
		fx.Annotated{
			Name:   key,
			Target: Unmarshal(key, defaults),
		},
	)
}
