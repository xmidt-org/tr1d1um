/**
 * Copyright 2022 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"fmt"
	"os"

	"github.com/spf13/pflag"
	"github.com/spf13/viper"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/sallust"
	"go.uber.org/zap"
)

func setupFlagSet(fs *pflag.FlagSet) {
	fs.StringP("file", "f", "", "the configuration file to use.  Overrides the search path.")
	fs.BoolP("debug", "d", false, "enables debug logging.  Overrides configuration.")
	fs.BoolP("version", "v", false, "print version and exit")
}

func setup(args []string) (*viper.Viper, *zap.Logger, *pflag.FlagSet, error) {
	l, err := zap.NewDevelopment() // initial value
	if err != nil {
		return nil, l, nil, fmt.Errorf("failed to create zap logger: %w", err)
	}

	fs := pflag.NewFlagSet(applicationName, pflag.ContinueOnError)
	setupFlagSet(fs)
	err = fs.Parse(args)
	if err != nil {
		return nil, l, fs, fmt.Errorf("failed to create parse args: %w", err)
	}
	if printVersion, _ := fs.GetBool("version"); printVersion {
		printVersionInfo(os.Stdout)
	}

	v := viper.New()

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
		return v, l, fs, fmt.Errorf("failed to read config file: %w", err)
	}

	if debug, _ := fs.GetBool("debug"); debug {
		v.Set("log.level", "DEBUG")
	}

	var c sallust.Config
	err = v.UnmarshalKey("logging", &c, arrange.ComposeDecodeHooks(sallust.DecodeHook))
	if err != nil {
		return v, l, fs, err
	}

	l, err = c.Build()
	return v, l, fs, err
}
