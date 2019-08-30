package translation

import (
	"context"
	"testing"

	"github.com/xmidt-org/wrp-go/wrp"
)

func TestMakeTranslationEndpoint(t *testing.T) {
	var s = new(MockService)

	r := &wrpRequest{
		WRPMessage:      new(wrp.Message),
		AuthHeaderValue: "a0",
	}

	s.On("SendWRP", r.WRPMessage, r.AuthHeaderValue).Return(nil, nil)

	e := makeTranslationEndpoint(s)
	e(context.TODO(), r)
	s.AssertExpectations(t)
}
