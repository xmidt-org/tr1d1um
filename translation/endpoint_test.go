// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"context"
	"testing"

	"github.com/xmidt-org/wrp-go/v3"
)

func TestMakeTranslationEndpoint(t *testing.T) {
	var s = new(MockService)

	r := &wrpRequest{
		WRPMessage:      new(wrp.Message),
		AuthHeaderValue: "a0",
	}

	s.On("SendWRP", context.TODO(), r.WRPMessage, r.AuthHeaderValue).Return(nil, nil)

	e := makeTranslationEndpoint(s)
	e(context.TODO(), r)
	s.AssertExpectations(t)
}
