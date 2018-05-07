package stat

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestStat(t *testing.T) {
	assert := assert.New(t)
	var authHeaderValue, actualURL string

	s := NewService(&ServiceOptions{
		XmidtStatURL: "http://localhost:8090/${device}/stat",
		CtxTimeout:   time.Second,
		Do:
		//capture sent values of interest
		func(r *http.Request) (*http.Response, error) {
			actualURL, authHeaderValue = r.URL.String(), r.Header.Get("Authorization")
			return nil, nil
		},
	})

	resp, err := s.RequestStat("a0", "mac:112233445566")

	assert.Nil(err)
	assert.Nil(resp)

	//verify correct header values are set in request
	assert.EqualValues("a0", authHeaderValue)

	//verify source in WRP message
	assert.EqualValues("http://localhost:8090/mac:112233445566/stat", actualURL)
}
