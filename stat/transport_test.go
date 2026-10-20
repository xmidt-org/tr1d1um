// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v5"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var ctxTID = context.WithValue(context.Background(), transaction.ContextKeyRequestTID, "testTID")

func TestDecodeRequest(t *testing.T) {

	t.Run("InvalidDeviceName", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:1122@#8!!"})

		resp, err := decodeRequest(ctxTID, r)

		assert.Nil(resp)
		assert.ErrorIs(err, wrp.ErrorInvalidDeviceName)

	})

	t.Run("NormalFlow", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/stat", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		r.Header.Set("Authorization", "a0")

		resp, err := decodeRequest(ctxTID, r)

		require.NoError(t, err)

		assert.Equal(&statRequest{
			DeviceID: "mac:112233445566",
		}, resp)
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

		assert.Equal(http.StatusInternalServerError, w.Code)
		assert.Equal(expected.String(), w.Body.String())
	})
}

func testErrorEncode(t *testing.T, expectedCode int, es []error) {
	t.Helper()
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

		assert.Equal(expectedCode, w.Code)
		assert.Equal(expected.String(), w.Body.String())
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
			desc: "Base Success",
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
			// Tr1d1um just forwards the response
			var e = encodeResponse(ctxTID, tc.respRecorder, tc.resp)
			if tc.expectedErr != nil {
				assert.ErrorIs(e, tc.expectedErr,
					fmt.Errorf("error [%v] doesn't contain error [%v] in its err chain",
						e, tc.expectedErr))
				return
			}
			require.NoError(t, e)
			assert.Equal(tc.expectedContentTypeHeader, tc.respRecorder.Header().Get("Content-Type"))
			assert.EqualValues(tc.resp.Body, tc.respRecorder.Body.String())
			assert.Equal(tc.resp.Code, tc.respRecorder.Code)
		})
	}
}

// TestEncodeResponse_ContentType runs encodeResponse behind a real server, so
// the response is what a caller sees.  Only a 200 from XMiDT is labeled
// JSON: any other response's body is passed through as is, so a JSON label
// set earlier is removed and the body's type is sniffed instead.
func TestEncodeResponse_ContentType(t *testing.T) {
	tests := []struct {
		name            string
		code            int
		body            string
		wantContentType string
	}{
		{
			name:            "200 is JSON",
			code:            http.StatusOK,
			body:            `{"dBytesSent": "1024"}`,
			wantContentType: "application/json",
		}, {
			name:            "404 is not labeled JSON",
			code:            http.StatusNotFound,
			body:            "device not found",
			wantContentType: "text/plain; charset=utf-8",
		}, {
			name:            "503 with a JSON looking body is not labeled JSON",
			code:            http.StatusServiceUnavailable,
			body:            `{"message": "unavailable"}`,
			wantContentType: "text/plain; charset=utf-8",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				// as if something earlier had labeled the response
				w.Header().Set("Content-Type", "application/json")

				err := encodeResponse(ctxTID, w, &transaction.XmidtResponse{
					Code:             tc.code,
					ForwardedHeaders: http.Header{"X-Xmidt-Message": {"forwarded"}},
					Body:             []byte(tc.body),
				})
				assert.NoError(t, err)
			}))
			defer srv.Close()

			resp, err := srv.Client().Get(srv.URL)
			require.NoError(t, err)
			defer resp.Body.Close()

			body, err := io.ReadAll(resp.Body)
			require.NoError(t, err)

			assert.Equal(t, tc.code, resp.StatusCode)
			assert.Equal(t, tc.body, string(body))
			assert.Equal(t, []string{tc.wantContentType}, resp.Header.Values("Content-Type"))
			assert.Equal(t, "forwarded", resp.Header.Get("X-Xmidt-Message"))
			assert.Equal(t, "testTID", resp.Header.Get(candlelight.HeaderWPATIDKeyName))
		})
	}
}
