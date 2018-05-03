package translation

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

	"github.com/Comcast/webpa-common/wrp"

	"github.com/stretchr/testify/assert"
)

func TestRequestGetPayload(t *testing.T) {
	t.Run("EmptyNames", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("", "")
		assert.EqualValues(ErrEmptyNames, e)
		assert.Nil(p)
	})

	t.Run("GET", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("n0,n1", "")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&getWDMP{Command: CommandGet, Names: []string{"n0", "n1"}})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})

	t.Run("GETAttrs", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("n0,n1", "attr0")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&getWDMP{Command: CommandGetAttrs, Names: []string{"n0", "n1"}, Attributes: "attr0"})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})
}

func TestRequestAddPayload(t *testing.T) {
	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestAddPayload(nil, nil)
		assert.Nil(p)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("RowNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestAddPayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(""))

		assert.Nil(p)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestAddPayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(`{"row": "r0"}`))

		assert.Nil(e)

		expected, err := json.Marshal(&addRowWDMP{
			Command: CommandAddRow,
			Table:   "t0",
			Row:     map[string]string{"row": "r0"},
		})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expected, p)
	})
}

func TestRequestReplacePayload(t *testing.T) {
	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestReplacePayload(nil, nil)
		assert.Nil(p)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("RowsNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestReplacePayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(""))

		assert.Nil(p)
		assert.EqualValues(ErrMissingRows, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)

		rowsPayload := `{"0": {"row": "r0"}}`

		p, e := requestReplacePayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(rowsPayload))

		assert.Nil(e)

		expected, err := json.Marshal(&replaceRowsWDMP{
			Command: CommandReplaceRows,
			Table:   "t0",
			Rows:    indexRow{"0": map[string]string{"row": "r0"}},
		})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expected, p)
	})
}

func TestRequestDeletePayload(t *testing.T) {
	t.Run("NoRowProvided", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestDeletePayload(nil)

		assert.Nil(p)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)

		expected, err := json.Marshal(&deleteRowDMP{Command: CommandDeleteRow,
			Row: "0",
		})
		if err != nil {
			panic(err)
		}

		p, e := requestDeletePayload(map[string]string{"parameter": "0"})

		assert.Nil(e)
		assert.EqualValues(expected, p)
	})
}

func TestEncodeResponse(t *testing.T) {
	assert := assert.New(t)

	//XMiDT response status code is not 200.
	//Tr1d1um should just forward such response code and body
	t.Run("StatusNotOK", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		response := &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Body:       ioutil.NopCloser(bytes.NewBufferString("t")),
			Header:     http.Header{"X-test": []string{"test"}},
		}

		err := encodeResponse(context.TODO(), recorder, response)

		assert.Nil(err)
		assert.EqualValues(http.StatusServiceUnavailable, recorder.Code)
		assert.EqualValues("t", recorder.Body.String())
		assert.EqualValues("test", recorder.Header().Get("X-test"))
	})

	//XMiDT response is not msgpack-encoded
	//Since this is not expected, Tr1d1um considers it an internal error case
	t.Run("UnexpectedResponseFormat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		response := &http.Response{
			StatusCode: http.StatusOK,
			Body:       ioutil.NopCloser(bytes.NewBufferString("t")),
		}

		assert.NotNil(encodeResponse(context.TODO(), recorder, response))
	})

	//XMiDt responds with a 200 (OK) with a well-formatted RDK device response
	//Tr1d1um returns the status provided by the device
	t.Run("RDKDeviceResponse", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		response := &http.Response{
			StatusCode: http.StatusOK,
			Body: ioutil.NopCloser(bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: []byte(`{"statusCode": 520}`),
			}, wrp.Msgpack))),
		}

		err := encodeResponse(context.TODO(), recorder, response)
		assert.Nil(err)
		assert.EqualValues(520, recorder.Code)
		assert.EqualValues(`{"statusCode": 520}`, recorder.Body.String())
	})

	//RDK device is having an internal error and returns 500.
	//Tr1d1um, in order to avoid ambiguity, should not return 500.
	//Rationale: Tr1d1um is not the one having an internal error, it is the device.
	t.Run("InternalRDKDeviceError", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		internalErrorResponse := []byte(`{"statusCode": 500, "message": "I, the device, suffer"}`)

		response := &http.Response{
			StatusCode: http.StatusOK,
			Body: ioutil.NopCloser(bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: internalErrorResponse}, wrp.Msgpack))),
		}

		err := encodeResponse(context.TODO(), recorder, response)
		assert.Nil(err)
		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(internalErrorResponse, recorder.Body.Bytes())
	})

	//For whatever reason, the device may respond with incomplete or unexpected data
	//In such case, Tr1d1um just forwards as much as it could from the device
	t.Run("BadRDKDeviceResponse", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		response := &http.Response{
			StatusCode: http.StatusOK,
			Body: ioutil.NopCloser(bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: []byte(`{"statusCode":`),
			}, wrp.Msgpack))),
		}

		err := encodeResponse(context.TODO(), recorder, response)
		assert.Nil(err)
		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(`{"statusCode":`, recorder.Body.String())
	})
}

func TestEncodeError(t *testing.T) {
	t.Run("BadRequests", func(t *testing.T) {
		assert := assert.New(t)

		for _, e := range []error{
			ErrEmptyNames,
			ErrInvalidService,
			ErrInvalidSetWDMP,
			ErrMissingRow,
			ErrMissingRows,
			ErrMissingTable,
			ErrNewCIDRequired,
		} {
			w := httptest.NewRecorder()

			expected := bytes.NewBufferString("")
			json.NewEncoder(expected).Encode(map[string]string{
				"message": e.Error()})

			encodeError(context.TODO(), e, w)
			assert.EqualValues(expected.String(), w.Body.String())
		}
	})

	t.Run("Timeouts", func(t *testing.T) {
		assert := assert.New(t)

		for _, e := range []error{
			context.DeadlineExceeded,
			context.Canceled,
		} {
			w := httptest.NewRecorder()

			expected := bytes.NewBufferString("")
			json.NewEncoder(expected).Encode(map[string]string{
				"message": e.Error()})

			encodeError(context.TODO(), e, w)
			assert.EqualValues(expected.String(), w.Body.String())
		}
	})

	t.Run("InternalError", func(t *testing.T) {
		assert := assert.New(t)

		w := httptest.NewRecorder()
		encodeError(context.TODO(), errors.New("something internal went unexpecting wrong"), w)

		expected := bytes.NewBufferString("")
		json.NewEncoder(expected).Encode(map[string]string{
			"message": common.ErrTr1d1umInternal.Error()})

		assert.EqualValues(expected.String(), w.Body.String())
	})
}
