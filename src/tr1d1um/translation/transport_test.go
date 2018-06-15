package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"tr1d1um/common"

	"github.com/Comcast/webpa-common/wrp"

	"github.com/stretchr/testify/assert"
)

//ctxTID is a context with a defined value for a TID
var ctxTID = context.WithValue(context.Background(), common.ContextKeyRequestTID, "test-tid")

func TestDecodeRequest(t *testing.T) {
	t.Run("PayloadFailure", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		_, e := decodeRequest(ctxTID, r)
		assert.EqualValues(ErrEmptyNames, e)
	})

	t.Run("WRPWrapFailure", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		wrpMsg, e := decodeRequest(ctxTID, r)
		assert.Nil(e)
		assert.NotEmpty(wrpMsg)
	})

	t.Run("Ideal", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		wrpMsg, e := decodeRequest(ctxTID, r)
		assert.Nil(e)
		assert.NotEmpty(wrpMsg)

	})
}
func TestRequestPayload(t *testing.T) {
	t.Run("Get", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrEmptyNames, e)
	})

	t.Run("Set", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPatch, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrInvalidSetWDMP, e)
	})

	t.Run("Del", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodDelete, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("Replace", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPut, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("Iot", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPost, "http://localhost", nil)

		r = mux.SetURLVars(r, map[string]string{"service": "iot"})

		p, e := requestPayload(r)
		assert.Empty(p)
		assert.Nil(e)
	})

	t.Run("Add", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPost, "http://localhost", nil)

		r = mux.SetURLVars(r, map[string]string{"service": "add"})
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("Others", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodOptions, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrUnsupportedMethod, e)
	})
}

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

func TestRequestSetPayload(t *testing.T) {
	t.Run("ErrAtDeduction", func(t *testing.T) {
		assert := assert.New(t)
		_, e := requestSetPayload(bytes.NewBufferString(""), "", "old", "sync")

		assert.EqualValues(ErrNewCIDRequired, e)
	})

	t.Run("InvalidWDMP", func(t *testing.T) {
		assert := assert.New(t)
		_, e := requestSetPayload(bytes.NewBufferString(""), "", "", "")

		assert.EqualValues(ErrInvalidSetWDMP, e)
	})

	t.Run("Ideal", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestSetPayload(bytes.NewBufferString(""), "new", "old", "sync")

		wdmp := new(setWDMP)
		err := json.NewDecoder(bytes.NewBuffer(p)).Decode(wdmp)

		if err != nil {
			panic(err)
		}

		assert.Nil(e)
		assert.EqualValues(CommandTestSet, wdmp.Command)
		assert.EqualValues("new", wdmp.NewCid)
		assert.EqualValues("old", wdmp.OldCid)
		assert.EqualValues("sync", wdmp.SyncCmc)
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
		response := &xmidtResponse{
			Code:             http.StatusServiceUnavailable,
			Body:             []byte("t"),
			ForwardedHeaders: http.Header{"X-test": []string{"test"}},
		}

		err := encodeResponse(ctxTID, recorder, response)

		assert.Nil(err)
		assert.EqualValues(http.StatusServiceUnavailable, recorder.Code)
		assert.EqualValues("t", recorder.Body.String())
		assert.EqualValues("test", recorder.Header().Get("X-test"))
	})

	//XMiDT response is not msgpack-encoded
	//Since this is not expected, Tr1d1um considers it an internal error case
	t.Run("UnexpectedResponseFormat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		response := &xmidtResponse{
			Code: http.StatusOK,
			Body: []byte("t"),
		}

		assert.NotNil(encodeResponse(ctxTID, recorder, response))
	})

	//XMiDt responds with a 200 (OK) with a well-formatted RDK device response
	//Tr1d1um returns the status provided by the device
	t.Run("RDKDeviceResponse", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		response := &xmidtResponse{
			Code: http.StatusOK,
			Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: []byte(`{"statusCode": 520}`),
			}, wrp.Msgpack)).Bytes(),
		}

		err := encodeResponse(ctxTID, recorder, response)
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

		response := &xmidtResponse{
			Code: http.StatusOK,
			Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: internalErrorResponse}, wrp.Msgpack)).Bytes(),
		}

		err := encodeResponse(ctxTID, recorder, response)
		assert.Nil(err)
		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(internalErrorResponse, recorder.Body.Bytes())
	})

	//For whatever reason, the device may respond with incomplete or unexpected data
	//In such case, Tr1d1um just forwards as much as it could from the device
	t.Run("BadRDKDeviceResponse", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		response := &xmidtResponse{
			Code: http.StatusOK,
			Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: []byte(`{"statusCode":`),
			}, wrp.Msgpack)).Bytes(),
		}

		err := encodeResponse(ctxTID, recorder, response)
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

			encodeError(ctxTID, e, w)
			assert.EqualValues(expected.String(), w.Body.String())
			assert.EqualValues(http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Timeouts", func(t *testing.T) {
		assert := assert.New(t)

		for _, e := range []error{
			common.NewCodedError(errors.New("some network error"), http.StatusServiceUnavailable),
			common.NewCodedError(errors.New("deadline exceeded"), http.StatusServiceUnavailable),
		} {
			w := httptest.NewRecorder()

			expected := bytes.NewBufferString("")
			json.NewEncoder(expected).Encode(map[string]string{
				"message": e.Error()})

			encodeError(ctxTID, e, w)
			assert.EqualValues(expected.String(), w.Body.String())
			assert.EqualValues(http.StatusServiceUnavailable, w.Code)
		}
	})

	t.Run("InternalError", func(t *testing.T) {
		assert := assert.New(t)

		w := httptest.NewRecorder()
		encodeError(ctxTID, errors.New("something internal went unexpecting wrong"), w)

		expected := bytes.NewBufferString("")
		json.NewEncoder(expected).Encode(map[string]string{
			"message": common.ErrTr1d1umInternal.Error()})

		assert.EqualValues(expected.String(), w.Body.String())
	})
}
