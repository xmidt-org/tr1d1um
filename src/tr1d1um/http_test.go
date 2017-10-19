package main

import (
	"bytes"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const errMsg = "shared failure"

var (
	payload, body                            = []byte("SomePayload"), bytes.NewBufferString("body")
	resp                                     = &http.Response{}
	mockConversion, mockEncoding, mockSender = &MockConversionTool{}, &MockEncodingTool{}, &MockSendAndHandle{}
	fakeLogger                               = &LightFakeLogger{}
	ch                                       = &ConversionHandler{
		wdmpConvert:    mockConversion,
		sender:         mockSender,
		encodingHelper: mockEncoding,
		logger:         fakeLogger,
	}
)

func TestConversionHandler(t *testing.T) {
	assert := assert.New(t)
	commonURL := "http://device/config?"
	commonRequest := httptest.NewRequest(http.MethodGet, commonURL, nil)
	var vars Vars = mux.Vars(commonRequest)

	t.Run("ErrDataParse", func(testing *testing.T) {
		mockConversion.On("GetFlavorFormat", commonRequest, vars, "attributes", "names", ",").
			Return(&GetWDMP{}, errors.New(errMsg)).Once()

		recorder := httptest.NewRecorder()
		ch.ServeHTTP(recorder, commonRequest)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)

		mockConversion.AssertExpectations(t)
	})

	t.Run("ErrEncode", func(testing *testing.T) {
		mockEncoding.On("EncodeJSON", wdmpGet).Return([]byte(""), errors.New(errMsg)).Once()
		mockConversion.On("GetFlavorFormat", commonRequest, vars, "attributes", "names", ",").
			Return(wdmpGet, nil).Once()

		recorder := httptest.NewRecorder()
		ch.ServeHTTP(recorder, commonRequest)

		mockEncoding.AssertExpectations(t)
		mockConversion.AssertExpectations(t)
	})

	t.Run("IdealGet", func(t *testing.T) {
		mockConversion.On("GetFlavorFormat", commonRequest, vars, "attributes", "names", ",").
			Return(wdmpGet, nil).Once()

		SetUpTest(wdmpGet, commonRequest)
		AssertCommonCalls(t)
	})

	t.Run("IdealSet", func(t *testing.T) {
		commonRequest = httptest.NewRequest(http.MethodPatch, commonURL, body)

		mockConversion.On("SetFlavorFormat", commonRequest).Return(wdmpSet, nil).Once()

		SetUpTest(wdmpSet, commonRequest)
		AssertCommonCalls(t)
	})

	t.Run("IdealAdd", func(t *testing.T) {
		commonRequest = httptest.NewRequest(http.MethodPost, commonURL, body)
		var urlVars Vars = mux.Vars(commonRequest)

		mockConversion.On("AddFlavorFormat", commonRequest.Body, urlVars, "parameter").
			Return(wdmpAdd, nil).Once()

		SetUpTest(wdmpAdd, commonRequest)
		AssertCommonCalls(t)
	})

	t.Run("IdealReplace", func(t *testing.T) {
		commonRequest = httptest.NewRequest(http.MethodPut, commonURL, body)
		var urlVars Vars = mux.Vars(commonRequest)

		mockConversion.On("ReplaceFlavorFormat", commonRequest.Body, urlVars, "parameter").
			Return(wdmpReplace, nil).Once()

		SetUpTest(wdmpReplace, commonRequest)
		AssertCommonCalls(t)
	})

	t.Run("IdealDelete", func(t *testing.T) {
		commonRequest = httptest.NewRequest(http.MethodDelete, commonURL, body)
		var urlVars Vars = mux.Vars(commonRequest)

		mockConversion.On("DeleteFlavorFormat", urlVars, "parameter").Return(wdmpDel, nil).Once()

		SetUpTest(wdmpDel, commonRequest)
		AssertCommonCalls(t)
	})
}

func SetUpTest(encodeArg interface{}, req *http.Request) {
	recorder := httptest.NewRecorder()
	timeout := time.Nanosecond

	mockEncoding.On("EncodeJSON", encodeArg).Return(payload, nil).Once()
	mockSender.On("Send", ch, recorder, payload, mock.AnythingOfType("*http.Request")).Return(resp, nil).Once()
	mockSender.On("HandleResponse", ch, nil, resp, recorder).Once()
	mockSender.On("GetRespTimeout").Return(timeout).Once()

	ch.ServeHTTP(recorder, req)
}

func AssertCommonCalls(t *testing.T) {
	mockConversion.AssertExpectations(t)
	mockEncoding.AssertExpectations(t)
	mockSender.AssertExpectations(t)
}

type LightFakeLogger struct{}

func (fake *LightFakeLogger) Log(_ ...interface{}) error {
	return nil
}
