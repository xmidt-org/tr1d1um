package stat

import (
	"context"
	"net/http"
	"strings"
	"time"
	"tr1d1um/common"
)

//Service defines the behavior of the Stat Tr1d1um Service
type Service interface {
	RequestStat(authHeaderValue, deviceID string) (*http.Response, error)
}

//NewService constructs a new stat service instance given some options
func NewService(o *ServiceOptions) Service {
	return &service{
		Do:           o.Do,
		XmidtStatURL: o.XmidtStatURL,
		CtxTimeout:   o.CtxTimeout,
	}
}

//ServiceOptions defines the options needed to build a new stat service
type ServiceOptions struct {
	//Do performs the HTTP transaction
	Do func(*http.Request) (*http.Response, error)

	//Base Endpoint URL for device stats from XMiDT API
	XmidtStatURL string

	//Deadline enforced on outgoing request
	CtxTimeout time.Duration
}

type service struct {
	Do func(*http.Request) (*http.Response, error)

	XmidtStatURL string

	CtxTimeout time.Duration
}

//RequestStat contacts the XMiDT cluster for device statistics
func (s *service) RequestStat(authHeaderValue, deviceID string) (resp *http.Response, err error) {
	var r *http.Request

	if r, err = http.NewRequest(http.MethodGet, strings.Replace(s.XmidtStatURL, "${device}", deviceID, 1), nil); err == nil {
		r.Header.Add("Authorization", authHeaderValue)

		ctx, cancel := context.WithTimeout(r.Context(), s.CtxTimeout)
		defer cancel()

		if resp, err = s.Do(r.WithContext(ctx)); err != nil {
			err = common.NewCodedError(err, http.StatusServiceUnavailable)
		}
	}
	return
}
