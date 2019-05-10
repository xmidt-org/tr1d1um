package stat

import (
	"net/http"
	"strings"

	"github.com/Comcast/tr1d1um/src/tr1d1um/common"
)

//Service defines the behavior of the device statistics Tr1d1um Service
type Service interface {
	RequestStat(authHeaderValue, deviceID string) (*common.XmidtResponse, error)
}

//NewService constructs a new stat service instance given some options
func NewService(o *ServiceOptions) Service {
	return &service{
		Tr1d1umTransactor: o.Tr1d1umTransactor,
		XmidtStatURL:      o.XmidtStatURL,
	}
}

//ServiceOptions defines the options needed to build a new stat service
type ServiceOptions struct {
	//Base Endpoint URL for device stats from XMiDT API
	XmidtStatURL string

	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about
	common.Tr1d1umTransactor
}

type service struct {
	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about
	common.Tr1d1umTransactor

	XmidtStatURL string
}

//RequestStat contacts the XMiDT cluster for device statistics
func (s *service) RequestStat(authHeaderValue, deviceID string) (result *common.XmidtResponse, err error) {
	var r *http.Request

	if r, err = http.NewRequest(http.MethodGet, strings.Replace(s.XmidtStatURL, "${device}", deviceID, 1), nil); err == nil {
		r.Header.Add("Authorization", authHeaderValue)

		result, err = s.Tr1d1umTransactor.Transact(r)
	}
	return
}
