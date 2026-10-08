// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"context"

	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v5"
)

type wrpRequest struct {
	WRPMessage      *wrp.Message
	AuthHeaderValue string
}

func makeTranslationEndpoint(s Service) func(context.Context, *wrpRequest) (*transaction.XmidtResponse, error) {
	return func(ctx context.Context, r *wrpRequest) (*transaction.XmidtResponse, error) {
		return s.SendWRP(ctx, r.WRPMessage, r.AuthHeaderValue)
	}
}
