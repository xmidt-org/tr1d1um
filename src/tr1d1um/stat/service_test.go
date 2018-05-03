package stat

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestStat(t *testing.T) {
	assert := assert.New(t)
	s := &SService{
		XMiDT:      "http://localhost:8090",
		CtxTimeout: time.Second,
	}

	var authHeaderValue, actualURL string

	//capture sent values of interest
	s.RetryDo = func(r *http.Request) (*http.Response, error) {
		actualURL, authHeaderValue = r.URL.String(), r.Header.Get("Authorization")
		return nil, nil
	}

	URI := "/api/v2/device/mac:112233445566/stat"
	resp, err := s.RequestStat("a0", URI)

	assert.Nil(err)
	assert.Nil(resp)

	//verify correct header values are set in request
	assert.EqualValues("a0", authHeaderValue)

	//verify source in WRP message
	assert.EqualValues(s.XMiDT+URI, actualURL)
}
