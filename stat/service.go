package stat

import (
	"context"
	"net/http"
	"time"
)

//Service defines the behavior of the Stat Tr1d1um Service
type Service interface {
	RequestStat(authVal string, URI string) ([]byte, error)
}

//SService is the Tr1d1um Service to serve device stats
type SService struct {
	RetryDo func(*http.Request) (*http.Response, error)

	XmidtURL string

	CtxTimeout time.Duration
}

//RequestStat contacts the XMiDT cluster requesting the statistics for a device specified in the uri
func (s *SService) RequestStat(authVal, URI string) (resp *http.Response, err error) {
	var r *http.Request
	if r, err = http.NewRequest(http.MethodGet, s.XmidtURL+URI, nil); err == nil {
		r.Header.Add("Authorization", authVal)

		ctx, cancel := context.WithTimeout(r.Context(), s.CtxTimeout)
		defer cancel()

		return s.RetryDo(r.WithContext(ctx))
	}
	return
}
