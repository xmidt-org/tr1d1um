package translation

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
)

type testContainer struct {
	//name of this test
	Name string

	//we use these to mock a remote call
	DoResponse *http.Response
	DoErr      error

	Expected *xmidtResponse
}

func TestSendWRP(t *testing.T) {
	var testCases = []testContainer{
		testContainer{
			Name: "Ideal",
			DoResponse: &http.Response{
				StatusCode: http.StatusOK,
				Body:       ioutil.NopCloser(bytes.NewBufferString("testBody")),
				Header:     http.Header{"X-": []string{"a", "b"}},
			},
			Expected: &xmidtResponse{
				Code:             http.StatusOK,
				Body:             []byte("testBody"),
				ForwardedHeaders: http.Header{"X-": []string{"a", "b"}},
			},
		},

		testContainer{
			Name:     "ClientDo error",
			DoErr:    errors.New("mock network error"),
			Expected: nil, //just to be explicit about expectation
		},
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
				WRPSource:   "local",
				Do: func(r *http.Request) (*http.Response, error) {
					//capture sent values of interest
					wrp.NewDecoder(r.Body, wrp.Msgpack).Decode(sentWRP)
					contentTypeValue, authHeaderValue = r.Header.Get("Content-Type"), r.Header.Get("Authorization")

					//return data we mocked
					return testCase.DoResponse, testCase.DoErr
				},
			})

			wrpMsg := &wrp.Message{
				TransactionUUID: "tid",
				Source:          "test",
			}

			result, err := w.SendWRP(wrpMsg, "auth")

			if testCase.DoErr == nil {
				assert.Nil(err)
			} else {
				assert.EqualValues(testCase.DoErr.Error(), err.Error())
			}

			assert.EqualValues(testCase.Expected, result)

			if testCase.DoResponse != nil {
				assert.EqualValues(testCase.DoResponse.StatusCode, result.Code)
			}

			//verify correct header values are set in request
			assert.EqualValues(wrp.Msgpack.ContentType(), contentTypeValue)
			assert.EqualValues("auth", authHeaderValue)

			//verify source in WRP message
			assert.EqualValues("local/test", sentWRP.Source)
		})
	}
}
