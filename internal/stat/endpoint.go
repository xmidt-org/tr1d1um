// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"context"

	"github.com/go-kit/kit/endpoint"
)

type statRequest struct {
	DeviceID        string
	AuthHeaderValue string
}

func makeStatEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, r interface{}) (interface{}, error) {
		statReq := (r).(*statRequest)
		return s.RequestStat(ctx, statReq.AuthHeaderValue, statReq.DeviceID)
	}
}
