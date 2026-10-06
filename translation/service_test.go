// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/wrp-go/v3"
)

func TestSendWRP(t *testing.T) {
	testCases := []struct {
		Name                string
		ExpectedRequestAuth string
		EnableAuth          bool
		MockError           error
	}{
		{
			Name:                "No auth acquirer",
			ExpectedRequestAuth: "",
		},

		{
			Name:                "Auth acquirer enabled - success",
			EnableAuth:          true,
			ExpectedRequestAuth: auth.MockAuthHeaderValue,
		},

		{
			Name:       "Auth acquirer enabled error",
			EnableAuth: true,
			MockError:  errors.New("error retrieving token"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			m := new(MockTr1d1umTransactor)
			var authDecorator *auth.MockDecorator

			options := &ServiceOptions{
				XmidtWrpURL: "http://localhost/wrp",
				WRPSource:   "dns:tr1d1um-xyz-example.com",
				T:           m,
			}

			if testCase.EnableAuth {
				authDecorator = new(auth.MockDecorator)
				options.Auth = authDecorator
				authDecorator.On("Decorate", mock.Anything, mock.Anything).Return(testCase.MockError).Once()
			}
			s := NewService(options)

			var expected = wrp.MustEncode(wrp.Message{
				Type:   wrp.SimpleRequestResponseMessageType,
				Source: "dns:tr1d1um-xyz-example.com",
			}, wrp.Msgpack)

			var requestMatcher = func(r *http.Request) bool {
				assert.EqualValues("http://localhost/wrp", r.URL.String())
				assert.EqualValues(testCase.ExpectedRequestAuth, r.Header.Get(basculehttp.DefaultAuthorizationHeader))
				assert.EqualValues(wrp.Msgpack.ContentType(), r.Header.Get("Content-Type"))

				data, err := io.ReadAll(r.Body)
				require.Nil(err)
				r.Body = io.NopCloser(bytes.NewBuffer(data))

				assert.EqualValues(string(expected), string(data))

				//MatchedBy is not friendly in explicitly showing what's not matching
				//so we use assertions instead in this function
				return true
			}

			if testCase.MockError != nil {
				m.AssertNotCalled(t, "Transact", mock.Anything)
			} else {
				m.On("Transact", mock.MatchedBy(requestMatcher)).Return(nil, nil)
			}

			_, e := s.SendWRP(context.TODO(), &wrp.Message{
				Type: wrp.SimpleRequestResponseMessageType,
			}, "pass-through-token")

			m.AssertExpectations(t)

			if testCase.EnableAuth {
				authDecorator.AssertExpectations(t)
				assert.Equal(testCase.MockError, e)
			} else {
				assert.Nil(e)
			}
		})
	}
}
