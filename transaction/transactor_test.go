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

package transaction

import (
	"bytes"
	"context"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest"
	"go.uber.org/zap/zaptest/observer"
)

func TestTransactError(t *testing.T) {
	assert := assert.New(t)

	plainErr := errors.New("network test error")
	expectedErr := NewCodedError(plainErr, 503)

	transactor := New(&Options{
		Do: func(_ *http.Request) (*http.Response, error) {
			return nil, plainErr
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	_, e := transactor.Transact(r)

	assert.EqualValues(expectedErr, e)
}

func TestTransactIdeal(t *testing.T) {
	assert := assert.New(t)

	expected := &XmidtResponse{
		Code:             404,
		Body:             []byte("not found"),
		ForwardedHeaders: http.Header{"X-A": []string{"a", "b"}},
	}

	rawXmidtResponse := &http.Response{
		StatusCode: 404,
		Body:       ioutil.NopCloser(bytes.NewBufferString("not found")),
		Header: http.Header{
			"X-A": []string{"a", "b"}, //should be forwarded
			"Y-A": []string{"c", "d"}, //should be ignored
		},
	}

	transactor := New(&Options{
		Do: func(_ *http.Request) (*http.Response, error) {
			return rawXmidtResponse, nil
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	actual, e := transactor.Transact(r)
	assert.Nil(e)
	assert.EqualValues(expected, actual)
}

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)

		var to, from = make(http.Header), make(http.Header)

		ForwardHeadersByPrefix("H", from, to)
		assert.Empty(to)
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		var to, from = make(http.Header), make(http.Header)

		from.Add("Helium", "3")
		from.Add("Hydrogen", "5")
		from.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("He", from, to)
		assert.NotEmpty(to)
		assert.Len(to, 1)
		assert.EqualValues("3", to.Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)

		var to, from = make(http.Header), make(http.Header)

		from.Add("Helium", "3")
		from.Add("Hydrogen", "5")
		from.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("H", from, to)
		assert.NotEmpty(to)
		assert.Len(to, 2)
		assert.EqualValues([]string{"5", "6"}, to["Hydrogen"])
	})

	t.Run("NilCases", func(t *testing.T) {
		var to, from = make(http.Header), make(http.Header)
		//none of these should panic
		ForwardHeadersByPrefix("", nil, nil)
		ForwardHeadersByPrefix("", from, nil)
		ForwardHeadersByPrefix("", from, to)
	})
}

func TestWelcome(t *testing.T) {
	tests := []struct {
		description string
		genReq      func() *http.Request
		expectedTID string
	}{
		{
			description: "Generated TID",
			genReq: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost", nil)
				return
			},
		},
		{
			description: "Given TID",
			genReq: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost", nil)
				r.Header.Set(candlelight.HeaderWPATIDKeyName, "tid01")
				return
			},
			expectedTID: "tid01",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)
			handler := http.HandlerFunc(
				func(_ http.ResponseWriter, r *http.Request) {
					assert.NotNil(r.Context().Value(ContextKeyRequestArrivalTime))
					tid := r.Context().Value(ContextKeyRequestTID)
					require.NotNil(tid)
					tid = tid.(string)
					if assert.NotZero(tid) && tc.expectedTID != "" {
						assert.Equal(tc.expectedTID, tid)
					}
				})
			decorated := Welcome(handler)
			decorated.ServeHTTP(nil, tc.genReq())

		})
	}
}

func TestLog(t *testing.T) {
	ctxWithArrivalTime := context.WithValue(context.Background(), ContextKeyRequestArrivalTime, time.Date(2009, time.November, 10, 23, 0, 0, 0, time.UTC))
	tcs := []struct {
		desc                        string
		reducedLoggingResponseCodes []int
		ctx                         context.Context
		code                        int
		request                     *http.Request
		expectedLogCount            int
	}{
		{
			desc:                        "Sanity Check",
			reducedLoggingResponseCodes: []int{},
			ctx:                         context.Background(),
			code:                        200,
			request:                     &http.Request{},
			expectedLogCount:            1,
		},
		{
			desc:                        "Arrival Time Present",
			reducedLoggingResponseCodes: []int{},
			ctx:                         ctxWithArrivalTime,
			code:                        200,
			request:                     &http.Request{},
			expectedLogCount:            2,
		},
		{
			desc:                        "IncludeHeaders is False",
			reducedLoggingResponseCodes: []int{200},
			ctx:                         context.Background(),
			code:                        200,
			request:                     &http.Request{},
			expectedLogCount:            1,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			var logCount = 0
			logger := zaptest.NewLogger(t, zaptest.WrapOptions(zap.Hooks(
				func(e zapcore.Entry) error {
					logCount++
					return nil
				})))
			ctx := sallust.With(tc.ctx, logger)
			s := Log(tc.reducedLoggingResponseCodes)
			s(ctx, tc.code, tc.request)
			assert.Equal(tc.expectedLogCount, logCount)
		})
	}
}

func TestAddDeviceIdToLog(t *testing.T) {
	tests := []struct {
		desc     string
		ctx      context.Context
		req      func() (r *http.Request)
		deviceid string
	}{
		{
			desc: "device id in request",
			ctx:  context.Background(),
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445577"})
				return
			},
			deviceid: "mac:112233445577",
		},
		{
			desc: "device id added in code",
			ctx:  context.Background(),
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				return
			},
			deviceid: "mac:000000000000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			observedZapCore, observedLogs := observer.New(zap.DebugLevel)
			observedLogger := zap.New(observedZapCore)
			ctx := sallust.With(tc.ctx, observedLogger)
			ctx = addDeviceIdToLog(ctx, tc.req())

			logger := sallust.Get(ctx)
			logger.Debug("test")
			gotLog := observedLogs.All()[0].Context

			assert.Equal("deviceid", gotLog[0].Key)
			assert.Equal(tc.deviceid, gotLog[0].String)

		})
	}
}

func TestGetDeviceId(t *testing.T) {
	tests := []struct {
		desc     string
		req      func() *http.Request
		expected string
	}{
		{
			desc: "Request has id",
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445577"})
				return
			},
			expected: "mac:112233445577",
		},
		{
			desc: "no id",
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				return
			},
			expected: "mac:000000000000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			id := getDeviceId(tc.req())
			assert.NotNil(id)
			assert.Equal(tc.expected, id)
		})
	}
}
