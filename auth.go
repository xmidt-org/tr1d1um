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
	"errors"
	"net/url"
	"strings"

	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculechecks"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/clortho"
	"go.uber.org/fx"
)

var possiblePrefixURLs = []string{
	"/" + apiBase,
	"/" + prevAPIBase,
}

// JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// Config is used to create the clortho Resolver & Refresher for JWT verification keys
	Config clortho.Config

	// Leeway is used to set the amount of time buffer should be given to JWT
	// time values, such as nbf
	Leeway bascule.Leeway
}

func provideAuthChain(configKey string) fx.Option {
	return fx.Options(
		basculehttp.ProvideMetrics(),
		basculechecks.ProvideMetrics(),
		fx.Provide(
			func() basculehttp.ParseURL {
				return createRemovePrefixURLFuncLegacy(possiblePrefixURLs)
			},
			arrange.UnmarshalKey("jwtValidator", JWTValidator{}),
			func(c JWTValidator) clortho.Config {
				return c.Config
			},
		),
		basculehttp.ProvideBasicAuth(configKey),
		basculehttp.ProvideBearerTokenFactory("jwtValidator", false),
		basculechecks.ProvideRegexCapabilitiesValidator("capabilityCheck"),
		basculehttp.ProvideBearerValidator(),
		basculehttp.ProvideServerChain(),
	)
}

func createRemovePrefixURLFuncLegacy(prefixes []string) basculehttp.ParseURL {
	return func(u *url.URL) (*url.URL, error) {
		escapedPath := u.EscapedPath()
		var prefix string
		for _, p := range prefixes {
			if strings.HasPrefix(escapedPath, p) {
				prefix = p
				break
			}
		}
		if prefix == "" {
			return nil, errors.New("unexpected URL, did not start with expected prefix")
		}
		u.Path = escapedPath[len(prefix):]
		u.RawPath = escapedPath[len(prefix):]
		return u, nil
	}
}
