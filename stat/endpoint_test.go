// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"context"
	"testing"
)

func TestMakeStatEndpoint(t *testing.T) {
	s := new(MockService)
	endpoint := makeStatEndpoint(s)

	sr := &statRequest{
		DeviceID: "mac:1122334455",
	}

	s.On("RequestStat", context.TODO(), "mac:1122334455").Return(nil, nil)

	endpoint(context.TODO(), sr)
	s.AssertExpectations(t)
}
