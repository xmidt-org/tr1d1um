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
