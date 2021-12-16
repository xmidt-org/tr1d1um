/**
 * Copyright 2021 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package stat

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/tr1d1um/transaction"
)

func TestRequestStat(t *testing.T) {
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
			Name:                "Auth acquirer enabled - error",
			EnableAcquirer:      true,
			AcquirerReturnError: errors.New("error retrieving token"),
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.Name, func(t *testing.T) {
			assert := assert.New(t)
			m := new(MockTr1d1umTransactor)
			var a *mockAcquirer

			options := &ServiceOptions{
				XmidtStatURL:   "http://localhost/stat/${device}",
				HTTPTransactor: m,
			}

			if testCase.EnableAcquirer {
				a = new(mockAcquirer)
				options.AuthAcquirer = a

				var err error = testCase.AcquirerReturnError
				a.On("Acquire").Return(testCase.AcquirerReturnString, err)
			}

			s := NewService(options)

			var requestMatcher = func(r *http.Request) bool {
				return r.URL.String() == "http://localhost/stat/mac:112233445566" &&
					r.Header.Get("Authorization") == testCase.ExpectedRequestAuth
			}

			if testCase.AcquirerReturnError != nil {
				m.AssertNotCalled(t, "Transact", mock.Anything)
			} else {
				m.On("Transact", mock.MatchedBy(requestMatcher)).Return(&transaction.XmidtResponse{}, nil)
			}

			_, e := s.RequestStat(context.TODO(), "pass-through-token", "mac:112233445566")

			m.AssertExpectations(t)
			if testCase.EnableAcquirer {
				a.AssertExpectations(t)
				if testCase.AcquirerReturnError != nil {
					assert.Equal(testCase.AcquirerReturnError, e)
				}
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
