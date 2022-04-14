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

	"github.com/xmidt-org/bascule/basculechecks"
	"github.com/xmidt-org/bascule/basculehttp"
	"go.uber.org/fx"
)

func provideAuthChain(configKey string) fx.Option {
	return fx.Options(
		basculehttp.ProvideMetrics(),
		basculechecks.ProvideMetrics(),
		fx.Provide(
			func() basculehttp.ParseURL {
				return basculehttp.CreateRemovePrefixURLFunc("/"+apiBase, nil)
			},
		),
		basculehttp.ProvideBasicAuth(configKey),
		basculehttp.ProvideBearerTokenFactory(configKey+".jwtValidator", false),
		basculechecks.ProvideRegexCapabilitiesValidator(fmt.Sprintf("%v.capabilities", configKey)),
		basculehttp.ProvideBearerValidator(),
		basculehttp.ProvideServerChain(),
	)
}
