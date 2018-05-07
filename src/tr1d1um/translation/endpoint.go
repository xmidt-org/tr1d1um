package translation

import (
	"context"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/endpoint"
)

type wrpRequest struct {
	WRPMessage      *wrp.Message
	AuthHeaderValue string
}

func makeTranslationEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, request interface{}) (interface{}, error) {
		wrpReq := (request).(*wrpRequest)
		return s.SendWRP(wrpReq.WRPMessage, wrpReq.AuthHeaderValue)
	}
}
