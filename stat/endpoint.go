// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"context"

	"github.com/xmidt-org/tr1d1um/transaction"
)

type statRequest struct {
	DeviceID string
}

func makeStatEndpoint(s Service) func(context.Context, *statRequest) (*transaction.XmidtResponse, error) {
	return func(ctx context.Context, r *statRequest) (*transaction.XmidtResponse, error) {
		return s.RequestStat(ctx, r.DeviceID)
	}
}
