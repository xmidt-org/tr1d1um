// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/tr1d1um/transaction"
)

func TestRequestStat(t *testing.T) {
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
			Name:       "Auth acquirer enabled - error",
			EnableAuth: true,
			MockError:  errors.New("error retrieving token"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)
			m := new(MockTr1d1umTransactor)
			var authDecorator *auth.MockDecorator

			options := &ServiceOptions{
				XmidtStatURL:   "http://localhost/stat/${device}",
				HTTPTransactor: m,
			}

			if testCase.EnableAuth {
				authDecorator = new(auth.MockDecorator)
				options.Auth = authDecorator
				authDecorator.On("Decorate", mock.Anything, mock.Anything).Return(testCase.MockError).Once()
			}

			s := NewService(options)

			var requestMatcher = func(r *http.Request) bool {
				return r.URL.String() == "http://localhost/stat/mac:112233445566" &&
					r.Header.Get(basculehttp.DefaultAuthorizationHeader) == testCase.ExpectedRequestAuth
			}

			if testCase.MockError != nil {
				m.AssertNotCalled(t, "Transact", mock.Anything)
			} else {
				m.On("Transact", mock.MatchedBy(requestMatcher)).Return(&transaction.XmidtResponse{}, nil)
			}

			_, e := s.RequestStat(context.TODO(), "mac:112233445566")

			m.AssertExpectations(t)
			if testCase.EnableAuth {
				authDecorator.AssertExpectations(t)
				if testCase.MockError != nil {
					assert.Equal(testCase.MockError, e)
				}
			}
		})
	}
}
