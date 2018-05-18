package translation

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
)

func TestSendWRP(t *testing.T) {
	var testCases = []struct {
		Name       string
		DoResponse *http.Response
		DoError    error
	}{
		{"Ideal", nil, nil},
		{"Error", nil, errors.New("network error")},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)

			var (
				contentTypeValue, authHeaderValue string
				sentWRP                           = new(wrp.Message)
			)

			w := NewService(&ServiceOptions{
				XmidtWrpURL: "http://localhost:8090/api/v2",
				CtxTimeout:  time.Second,
				WRPSource:   "local",
				Do:

				//capture sent values of interest
				func(r *http.Request) (resp *http.Response, err error) {
					wrp.NewDecoder(r.Body, wrp.Msgpack).Decode(sentWRP)
					contentTypeValue, authHeaderValue = r.Header.Get("Content-Type"), r.Header.Get("Authorization")
					resp, err = testCase.DoResponse, testCase.DoError
					return
				},
			})

			wrpMsg := &wrp.Message{
				TransactionUUID: "tid",
				Source:          "test",
			}

			resp, err := w.SendWRP(wrpMsg, "auth")

			if testCase.DoError == nil {
				assert.Nil(err)
			} else {
				assert.EqualValues(testCase.DoError.Error(), err.Error())
			}

			assert.EqualValues(testCase.DoResponse, resp)

			//verify correct header values are set in request
			assert.EqualValues(wrp.Msgpack.ContentType(), contentTypeValue)
			assert.EqualValues("auth", authHeaderValue)

			//verify source in WRP message
			assert.EqualValues("local/test", sentWRP.Source)
		})
	}
}
