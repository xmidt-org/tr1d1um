package stat

import (
	"github.com/xmidt-org/bascule/acquire"
	"net/http"
	"strings"

	"github.com/xmidt-org/tr1d1um/common"
)

//Service defines the behavior of the device statistics Tr1d1um Service
type Service interface {
	RequestStat(authHeaderValue, deviceID string) (*common.XmidtResponse, error)
}

//NewService constructs a new stat service instance given some options
func NewService(o *ServiceOptions) Service {
	return &service{
		Tr1d1umTransactor: o.Tr1d1umTransactor,
		Acquirer:          o.Acquirer,
		XmidtStatURL:      o.XmidtStatURL,
	}
}

//ServiceOptions defines the options needed to build a new stat service
type ServiceOptions struct {
	//Base Endpoint URL for device stats from XMiDT API
	XmidtStatURL string

	acquire.Acquirer

	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about
	common.Tr1d1umTransactor
}

type service struct {
	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about
	common.Tr1d1umTransactor

	acquire.Acquirer

	XmidtStatURL string
}

//RequestStat contacts the XMiDT cluster for device statistics
func (s *service) RequestStat(authHeaderValue, deviceID string) (*common.XmidtResponse, error) {
	r, err := http.NewRequest(http.MethodGet, strings.Replace(s.XmidtStatURL, "${device}", deviceID, 1), nil)

	if err != nil {
		return nil, err
	}

	if s.Acquirer != nil {
		authHeaderValue, err = s.Acquirer.Acquire()
		if err != nil {
			return nil, err
		}
	}

	r.Header.Set("Authorization", authHeaderValue)
	return s.Tr1d1umTransactor.Transact(r)
}
