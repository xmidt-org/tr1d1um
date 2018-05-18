package stat

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestRequestStat(t *testing.T) {
	var testCases = []struct {
		Name       string
		DoResponse *http.Response
		DoError    error
	}{
		{"Ideal", nil, nil},
		{"Error", nil, context.DeadlineExceeded},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)
			var authHeaderValue, actualURL string

			s := NewService(&ServiceOptions{
				XmidtStatURL: "http://localhost:8090/${device}/stat",
				CtxTimeout:   time.Second,
				Do:
				//capture sent values of interest
				func(r *http.Request) (*http.Response, error) {
					actualURL, authHeaderValue = r.URL.String(), r.Header.Get("Authorization")
					return testCase.DoResponse, testCase.DoError
				},
			})

			resp, err := s.RequestStat("a0", "mac:112233445566")

			if testCase.DoError == nil {
				assert.Nil(err)
			} else {
				assert.EqualValues(testCase.DoError.Error(), err.Error())
			}

			assert.EqualValues(testCase.DoResponse, resp)

			//verify correct header values are set in request
			assert.EqualValues("a0", authHeaderValue)

			//verify URI
			assert.EqualValues("http://localhost:8090/mac:112233445566/stat", actualURL)
		})
	}
}
