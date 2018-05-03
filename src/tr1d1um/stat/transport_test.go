package stat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"tr1d1um/common"

	"github.com/gorilla/mux"

	"github.com/Comcast/webpa-common/device"

	"github.com/stretchr/testify/assert"
)

func TestDecodeRequest(t *testing.T) {

	t.Run("InvalidDeviceName", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:1122@#8!!"})

		resp, err := decodeRequest(context.TODO(), r)

		assert.Nil(resp)
		assert.Equal(device.ErrorInvalidDeviceName, err)

	})

	t.Run("NormalFlow", func(t *testing.T) {
		var assert = assert.New(t)

		var r = httptest.NewRequest(http.MethodGet, "http://localhost:8090/api/stat", nil)

		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		r.Header.Set("Authorization", "a0")

		resp, err := decodeRequest(context.TODO(), r)

		assert.Nil(err)

		assert.Equal(&statRequest{
			AuthValue: "a0",
			URI:       "/api/stat",
		}, resp.(*statRequest))
	})
}

func TestEncodeError(t *testing.T) {
	t.Run("Timeouts", func(t *testing.T) {
		testErrorEncode(t, http.StatusServiceUnavailable, []error{
			errors.New("somePrefix->Client.Timeout exceeded while<-someSuffix"), // mock an HTTP.Client.Timeout
			context.Canceled,
			context.DeadlineExceeded,
		})
	})

	t.Run("BadRequest", func(t *testing.T) {
		testErrorEncode(t, http.StatusBadRequest, []error{
			device.ErrorInvalidDeviceName,
		})
	})

	t.Run("Internal", func(t *testing.T) {
		assert := assert.New(t)
		expected := bytes.NewBufferString("")

		json.NewEncoder(expected).Encode(
			map[string]string{
				"message": common.ErrTr1d1umInternal.Error(),
			},
		)

		w := httptest.NewRecorder()
		encodeError(context.TODO(), errors.New("tremendously unexpected internal error"), w)

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
		encodeError(context.TODO(), e, w)

		assert.EqualValues(expectedCode, w.Code)
		assert.EqualValues(expected.String(), w.Body.String())
	}
}

func TestEncodeResponse(t *testing.T) {
	var assert = assert.New(t)

	var (
		w = httptest.NewRecorder()
		p = `{"dBytesSent": "1024"}`

		resp = &http.Response{
			StatusCode: http.StatusOK,
			Header: http.Header{
				"Content-Type": []string{"application/json"},
			},
			Body: ioutil.NopCloser(bytes.NewBufferString(p)),
		}
	)

	//Tr1d1um just forwards the response
	var e = encodeResponse(context.TODO(), w, resp)

	assert.Nil(e)
	assert.EqualValues(w.Header().Get("Content-Type"), resp.Header.Get("Content-Type"))
	assert.EqualValues(p, w.Body.String())
	assert.EqualValues(resp.StatusCode, w.Code)
}
