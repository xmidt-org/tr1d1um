package translation

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/xmidt-org/tr1d1um/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/wrp-go/wrp"
)

func TestSendWRP(t *testing.T) {
	testCases := []struct {
		Name                 string
		ExpectedRequestAuth  string
		EnableAcquirer       bool
		AcquirerReturnString string
		AcquirerReturnError  error
	}{
		{
			Name:                "No auth acquirer",
			ExpectedRequestAuth: "pass-through-token",
		},

		{
			Name:                 "Auth acquirer enabled - success",
			EnableAcquirer:       true,
			ExpectedRequestAuth:  "acquired-token",
			AcquirerReturnString: "acquired-token",
		},

		{
			Name:                "Auth acquirer enabled error",
			EnableAcquirer:      true,
			AcquirerReturnError: errors.New("error retrieving token"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			m := new(common.MockTr1d1umTransactor)
			var a *mockAcquirer

			options := &ServiceOptions{
				XmidtWrpURL:       "http://localhost/wrp",
				WRPSource:         "dns:tr1d1um-xyz-example.com",
				Tr1d1umTransactor: m,
			}

			if testCase.EnableAcquirer {
				a = new(mockAcquirer)
				options.Acquirer = a

				var err error = testCase.AcquirerReturnError
				a.On("Acquire").Return(testCase.AcquirerReturnString, err)
			}

			s := NewService(options)

			var expected = wrp.MustEncode(wrp.Message{
				Type:   wrp.SimpleRequestResponseMessageType,
				Source: "dns:tr1d1um-xyz-example.com",
			}, wrp.Msgpack)

			var requestMatcher = func(r *http.Request) bool {
				assert.EqualValues("http://localhost/wrp", r.URL.String())
				assert.EqualValues(testCase.ExpectedRequestAuth, r.Header.Get("Authorization"))
				assert.EqualValues(wrp.Msgpack.ContentType(), r.Header.Get("Content-Type"))

				data, err := ioutil.ReadAll(r.Body)
				require.Nil(err)
				r.Body = ioutil.NopCloser(bytes.NewBuffer(data))

				assert.EqualValues(string(expected), string(data))

				//MatchedBy is not friendly in explicitly showing what's not matching
				//so we use assertions instead in this function
				return true
			}

			if testCase.AcquirerReturnError != nil {
				m.AssertNotCalled(t, "Transact", mock.Anything)
			} else {
				m.On("Transact", mock.MatchedBy(requestMatcher)).Return(nil, nil)
			}

			_, e := s.SendWRP(&wrp.Message{
				Type: wrp.SimpleRequestResponseMessageType,
			}, "pass-through-token")

			m.AssertExpectations(t)

			if testCase.EnableAcquirer {
				a.AssertExpectations(t)
				assert.Equal(testCase.AcquirerReturnError, e)
			} else {
				assert.Nil(e)
			}
		})
	}
}

type mockAcquirer struct {
	mock.Mock
}

func (m *mockAcquirer) Acquire() (string, error) {
	args := m.Called()
	return args.String(0), args.Error(1)
}
