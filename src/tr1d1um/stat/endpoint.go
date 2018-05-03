package stat

import (
	"context"

	"github.com/go-kit/kit/endpoint"
)

type statRequest struct {
	URI       string
	AuthValue string
}

func makeStatEndpoint(s Service) endpoint.Endpoint {
	return func(ctx context.Context, r interface{}) (interface{}, error) {
		statReq := (r).(*statRequest)
		return s.RequestStat(statReq.AuthValue, statReq.URI)
	}
}
