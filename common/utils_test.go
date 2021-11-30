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

package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmidt-org/webpa-common/v2/logging"

	"github.com/stretchr/testify/assert"
)

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

func TestErrorLogEncoder(t *testing.T) {
	tcs := []struct {
		desc      string
		getLogger GetLoggerFunc
	}{
		{
			desc:      "nil getlogger",
			getLogger: nil,
		},
		{
			desc:      "valid getlogger",
			getLogger: GetLogger,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			e := func(ctx context.Context, _ error, _ http.ResponseWriter) {
				assert.EqualValues("tid00", ctx.Value(ContextKeyRequestTID))
			}
			le := ErrorLogEncoder(tc.getLogger, e)

			assert.NotPanics(func() {
				//assumes TID is context
				le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
			})
		})
	}
}

func TestWelcome(t *testing.T) {
	assert := assert.New(t)
	var handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NotNil(r.Context().Value(ContextKeyRequestArrivalTime))
	})

	decorated := Welcome(handler)
	req := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
	decorated.ServeHTTP(nil, req)
}

func TestCapture(t *testing.T) {
	t.Run("GivenTID", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		r.Header.Set(HeaderWPATID, "tid01")
		ctx := Capture(logging.NewTestLogger(nil, t))(context.TODO(), r)
		assert.EqualValues("tid01", ctx.Value(ContextKeyRequestTID).(string))
	})

	t.Run("GeneratedTID", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		ctx := Capture(logging.NewTestLogger(nil, t))(context.TODO(), r)
		assert.NotEmpty(ctx.Value(ContextKeyRequestTID).(string))
	})
}

func TestGenTID(t *testing.T) {
	assert := assert.New(t)
	tid := genTID()
	assert.NotEmpty(tid)
}
