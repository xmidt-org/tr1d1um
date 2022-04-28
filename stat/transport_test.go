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
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v3"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
)

var ctxTID = context.WithValue(context.Background(), transaction.ContextKeyRequestTID, "testTID")

func TestDecodeRequest(t *testing.T) {

	t.Run("InvalidDeviceName", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:1122@#8!!"})

		resp, err := decodeRequest(ctxTID, r)

		assert.Nil(resp)
		assert.Equal(wrp.ErrorInvalidDeviceName.Error(), err.Error())

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
			transaction.NewBadRequestError(wrp.ErrorInvalidDeviceName),
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

	tcs := []struct {
		desc                      string
		expectedErr               error
		expectedContentTypeHeader string
		resp                      *transaction.XmidtResponse
		respRecorder              *httptest.ResponseRecorder
	}{
		{
			desc: "Base Sucess",
			resp: &transaction.XmidtResponse{
				Code:             http.StatusOK,
				ForwardedHeaders: http.Header{},
				Body:             []byte(`{"dBytesSent": "1024"}`),
			},
			expectedContentTypeHeader: "application/json",
			respRecorder:              httptest.NewRecorder(),
		},
		{
			desc: "Response Body is Nil Failure",
			resp: &transaction.XmidtResponse{
				Code:             http.StatusOK,
				ForwardedHeaders: http.Header{},
			},
			expectedErr: errResponseIsNil,
		},
		{
			desc:        "Response is Nil Failure",
			expectedErr: errResponseIsNil,
		},
	}
	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			//Tr1d1um just forwards the response
			var e = encodeResponse(ctxTID, tc.respRecorder, tc.resp)
			if tc.expectedErr != nil {
				assert.True(errors.Is(e, tc.expectedErr),
					fmt.Errorf("error [%v] doesn't contain error [%v] in its err chain",
						e, tc.expectedErr))
			} else {
				assert.Nil(e)
				assert.EqualValues(tc.expectedContentTypeHeader, tc.respRecorder.Header().Get("Content-Type"))
				assert.EqualValues(tc.resp.Body, tc.respRecorder.Body.String())
				assert.EqualValues(tc.resp.Code, tc.respRecorder.Code)
			}
		})
	}
}
