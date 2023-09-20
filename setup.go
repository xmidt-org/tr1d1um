// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/xmidt-org/sallust"
	"go.uber.org/zap"
)

func setupFlagSet(fs *pflag.FlagSet) {
	fs.StringP("file", "f", "", "the configuration file to use.  Overrides the search path.")
	fs.BoolP("debug", "d", false, "enables debug logging.  Overrides configuration.")
	fs.BoolP("version", "v", false, "print version and exit")
}

func setup(args []string) (*viper.Viper, *zap.Logger, *pflag.FlagSet, error) {
	fs := pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
	setupFlagSet(fs)
	err := fs.Parse(args)
	if err != nil {
		return nil, nil, fs, fmt.Errorf("failed to create parse args: %w", err)
	}

	v := viper.New()
	for k, va := range defaults {
		v.SetDefault(k, va)
	}

	if file, _ := fs.GetString("file"); len(file) > 0 {
		v.SetConfigFile(file)
		err = v.ReadInConfig()
	} else {
		v.SetConfigName(applicationName)
		v.AddConfigPath(fmt.Sprintf("/etc/%s", applicationName))
		v.AddConfigPath(fmt.Sprintf("$HOME/.%s", applicationName))
		v.AddConfigPath(".")
		err = v.ReadInConfig()
	}
	if err != nil {
		return v, nil, fs, fmt.Errorf("failed to read config file: %w", err)
	}

	if debug, _ := fs.GetBool("debug"); debug {
		v.Set("log.level", "DEBUG")
	}

	var c sallust.Config
	v.UnmarshalKey("logging", &c)
	l := zap.Must(c.Build())
	return v, l, fs, err
}
