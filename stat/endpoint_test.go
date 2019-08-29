package stat

import (
	"context"
	"testing"
)

func TestMakeStatEndpoint(t *testing.T) {
	s := new(MockService)
	endpoint := makeStatEndpoint(s)

	sr := &statRequest{
		DeviceID:        "mac:1122334455",
		AuthHeaderValue: "a0",
	}

	s.On("RequestStat", "a0", "mac:1122334455").Return(nil, nil)

	endpoint(context.TODO(), sr)
	s.AssertExpectations(t)
}
