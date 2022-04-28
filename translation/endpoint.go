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

package translation

import (
	"context"

	"github.com/go-kit/kit/endpoint"
	"github.com/xmidt-org/wrp-go/v3"
)

type wrpRequest struct {
	WRPMessage      *wrp.Message
	AuthHeaderValue string
}

func makeTranslationEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		wrpReq := (request).(*wrpRequest)
		return s.SendWRP(ctx, wrpReq.WRPMessage, wrpReq.AuthHeaderValue)
	}
}
