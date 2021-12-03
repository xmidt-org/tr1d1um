package stat

import (
	"context"
	"net/http"
	"strings"

	"github.com/xmidt-org/bascule/acquire"

	"github.com/xmidt-org/tr1d1um/transaction"
)

// Service defines the behavior of the device statistics Tr1d1um Service.
type Service interface {
	RequestStat(ctx context.Context, authHeaderValue, deviceID string) (*transaction.XmidtResponse, error)
}

// NewService constructs a new stat service instance given some options.
func NewService(o *ServiceOptions) Service {
	return &service{
		transactor:   o.HTTPTransactor,
		authAcquirer: o.AuthAcquirer,
		xmidtStatURL: o.XmidtStatURL,
	}
}

// ServiceOptions defines the options needed to build a new stat service.
type ServiceOptions struct {
	//Base Endpoint URL for device stats from the XMiDT API.
	//It's expected to have the "${device}" substring to perform device ID substitution.
	XmidtStatURL string

	//AuthAcquirer provides a mechanism to fetch auth tokens to complete the HTTP transaction
	//with the remote server.
	//(Optional)
	AuthAcquirer acquire.Acquirer

	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about.
	HTTPTransactor transaction.Tr1d1umTransactor
}

type service struct {
	transactor transaction.Tr1d1umTransactor

	authAcquirer acquire.Acquirer

	xmidtStatURL string
}

// RequestStat contacts the XMiDT cluster for device statistics.
func (s *service) RequestStat(ctx context.Context, authHeaderValue, deviceID string) (*transaction.XmidtResponse, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.Replace(s.xmidtStatURL, "${device}", deviceID, 1), nil)

	if err != nil {
		return nil, err
	}

	if s.authAcquirer != nil {
		authHeaderValue, err = s.authAcquirer.Acquire()
		if err != nil {
			return nil, err
		}
	}

	r.Header.Set("Authorization", authHeaderValue)
	return s.transactor.Transact(r)
}
