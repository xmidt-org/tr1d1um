package main

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
	"context"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"gopkg.in/h2non/gock.v1"
)

func TestSend(t *testing.T) {
	assert := assert.New(t)

	data := []byte("data")
	WRPMsg := &wrp.Message{}
	WRPPayload := []byte("payload")
	validURL := "http://someValidURL"

	ch := &ConversionHandler{encodingHelper: mockEncoding, wdmpConvert: mockConversion, targetURL: validURL}

	t.Run("SendEncodeErr", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, validURL, nil)
		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, errors.New(errMsg)).Once()
		recorder := httptest.NewRecorder()
		tr1 := NewTR1()
		_, err := tr1.Send(ch, recorder, data, req)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("SendNewRequestErr", func(t *testing.T) {
		tr1 := NewTR1()
		tr1.NewHTTPRequest = NewHTTPRequestFail
		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()
		recorder := httptest.NewRecorder()
		_, err := tr1.Send(ch, recorder, data, req)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)

	})

	t.Run("SendIdeal", func(t *testing.T) {
		defer gock.OffAll()

		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()

		gock.New(validURL).Reply(http.StatusOK)
		tr1 := NewTR1()

		resp, err := tr1.Send(ch, nil, data, req)

		assert.Nil(err)
		assert.EqualValues(http.StatusOK, resp.StatusCode)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("SendTimeout", func(t *testing.T) {
		defer gock.OffAll()

		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()

		tr1 := NewTR1()
		ctx, cancel := context.WithCancel(req.Context())
		cancel() // fake a timeout through a cancel even before Send() begins

		gock.New(validURL).Reply(http.StatusOK).Delay(time.Second) // on purpose delaying response
		_, err := tr1.Send(ch, nil, data, req.WithContext(ctx))

		assert.NotNil(err)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
	})
}

func TestHandleResponse(t *testing.T) {
	assert := assert.New(t)
	tr1 := &Tr1SendAndHandle{log: &LightFakeLogger{}, client: &http.Client{Timeout: time.Second}}
	tr1.NewHTTPRequest = http.NewRequest

	ch := &ConversionHandler{encodingHelper: mockEncoding, wdmpConvert: mockConversion}

	t.Run("IncomingErr", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(nil, errors.New(errMsg), nil, recorder)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
	})

	t.Run("StatusNotOK", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		fakeResponse := &http.Response{StatusCode: http.StatusBadRequest}
		tr1.HandleResponse(nil, nil, fakeResponse, recorder)
		assert.EqualValues(http.StatusBadRequest, recorder.Code)
	})

	t.Run("ExtractPayloadFail", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.JSON).Return([]byte(""),
			errors.New(errMsg)).Once()
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder)

		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("IdealCase", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		extractedData := []byte("extract")

		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.JSON).Return(extractedData, nil).Once()
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder)

		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(extractedData, recorder.Body.Bytes())
		mockEncoding.AssertExpectations(t)
	})
}

func NewHTTPRequestFail(_, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New(errMsg)
}

func NewTR1() (tr1 *Tr1SendAndHandle) {
	tr1 = &Tr1SendAndHandle{log: &LightFakeLogger{}, client: &http.Client{Timeout: time.Second}}
	tr1.NewHTTPRequest = http.NewRequest
	return tr1
}
