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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmidt-org/tr1d1um/transaction"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/xmidt-org/webpa-common/v2/device"
)

var ctxTID = context.WithValue(context.Background(), transaction.ContextKeyRequestTID, "testTID")

func TestDecodeRequest(t *testing.T) {

	t.Run("InvalidDeviceName", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:1122@#8!!"})

		resp, err := decodeRequest(ctxTID, r)

		assert.Nil(resp)
		assert.Equal(device.ErrorInvalidDeviceName.Error(), err.Error())

	})

	t.Run("NormalFlow", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/stat", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		r.Header.Set("Authorization", "a0")

		resp, err := decodeRequest(ctxTID, r)

		assert.Nil(err)

		assert.Equal(&statRequest{
			AuthHeaderValue: "a0",
			DeviceID:        "mac:112233445566",
		}, resp.(*statRequest))
	})
}

func TestEncodeError(t *testing.T) {
	t.Run("Timeouts", func(t *testing.T) {
		testErrorEncode(t, http.StatusServiceUnavailable, []error{
			transaction.NewCodedError(errors.New("some bad network timeout error"), http.StatusServiceUnavailable),
		})
	})

	t.Run("BadRequest", func(t *testing.T) {
		testErrorEncode(t, http.StatusBadRequest, []error{
			transaction.NewBadRequestError(device.ErrorInvalidDeviceName),
		})
	})

	t.Run("Internal", func(t *testing.T) {
		assert := assert.New(t)
		expected := bytes.NewBufferString("")

		json.NewEncoder(expected).Encode(
			map[string]string{
				"message": transaction.ErrTr1d1umInternal.Error(),
			},
		)

		w := httptest.NewRecorder()
		encodeError(ctxTID, errors.New("tremendously unexpected internal error"), w)

		assert.EqualValues(http.StatusInternalServerError, w.Code)
		assert.EqualValues(expected.String(), w.Body.String())
	})
}

func testErrorEncode(t *testing.T, expectedCode int, es []error) {
	assert := assert.New(t)

	for _, e := range es {
		expected := bytes.NewBufferString("")
		json.NewEncoder(expected).Encode(
			map[string]string{
				"message": e.Error(),
			},
		)

		w := httptest.NewRecorder()
		encodeError(ctxTID, e, w)

		assert.EqualValues(expectedCode, w.Code)
		assert.EqualValues(expected.String(), w.Body.String())
	}
}

func TestEncodeResponse(t *testing.T) {
	var assert = assert.New(t)

	var (
		w = httptest.NewRecorder()
		p = []byte(`{"dBytesSent": "1024"}`)

		resp = &transaction.XmidtResponse{
			Code:             http.StatusOK,
			ForwardedHeaders: http.Header{},
			Body:             p,
		}
	)

	//Tr1d1um just forwards the response
	var e = encodeResponse(ctxTID, w, resp)

	assert.Nil(e)
	assert.EqualValues("application/json", w.Header().Get("Content-Type"))
	assert.EqualValues(p, w.Body.String())
	assert.EqualValues(resp.Code, w.Code)
}
