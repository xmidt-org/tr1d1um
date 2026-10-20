// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"fmt"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"go.uber.org/zap"

	"github.com/xmidt-org/sallust"
)

func setupFlagSet(fs *pflag.FlagSet) {
	fs.StringP("file", "f", "", "the configuration file to use.  Overrides the search path.")
	fs.BoolP("debug", "d", false, "enables debug logging.  Overrides configuration.")
	fs.BoolP("version", "v", false, "print version and exit")
}

// parseFlags parses tr1d1um's command line flags from args.
func parseFlags(args []string) (*pflag.FlagSet, error) {
	fs := pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
	setupFlagSet(fs)
	if err := fs.Parse(args); err != nil {
		return nil, fmt.Errorf("failed to parse args: %w", err)
	}

	return fs, nil
}

// setup reads the configuration and builds the logger, as the parsed flags
// direct.
func setup(fs *pflag.FlagSet) (*viper.Viper, *zap.Logger, error) {
	var err error

	v := viper.New()
	if file, _ := fs.GetString("file"); len(file) > 0 {
		v.SetConfigFile(file)
		err = v.ReadInConfig()
	} else {
		v.SetConfigName(applicationName)
		v.AddConfigPath("/etc/" + applicationName)
		v.AddConfigPath("$HOME/." + applicationName)
		v.AddConfigPath(".")
		err = v.ReadInConfig()
	}
	if err != nil {
		return v, nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var c sallust.Config
	if err := v.UnmarshalKey("logging", &c); err != nil {
		return v, nil, fmt.Errorf("failed to read logging config: %w", err)
	}

	// The level is set on the parsed configuration rather than in viper,
	// where setting logging.level would hide the rest of the logging section.
	if debug, _ := fs.GetBool("debug"); debug {
		c.Level = "debug"
	}
	l := zap.Must(c.Build())
	return v, l, err
}
