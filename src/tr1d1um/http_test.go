/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
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
	mockRequester                            = &MockRequester{}
	fakeLogger                               = &LightFakeLogger{}
	mockRequestValidator                     = &MockRequestValidator{}
	ch                                       = &ConversionHandler{
		wdmpConvert:      mockConversion,
		sender:           mockSender,
		encodingHelper:   mockEncoding,
		logger:           fakeLogger,
		RequestValidator: mockRequestValidator,
	}
)

func TestConversionHandler(t *testing.T) {
	assert := assert.New(t)
	commonURL := "http://device/config?"
	commonRequest := httptest.NewRequest(http.MethodGet, commonURL, nil)
	var vars Vars = mux.Vars(commonRequest)

	t.Run("ErrDataParse", func(testing *testing.T) {
		recorder := httptest.NewRecorder()

		mockConversion.On("GetFlavorFormat", commonRequest, vars, "attributes", "names", ",").
			Return(&GetWDMP{}, errors.New(errMsg)).Once()
		mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(true).Once()

		ch.ServeHTTP(recorder, commonRequest)
		assert.EqualValues(http.StatusBadRequest, recorder.Code)

		mockConversion.AssertExpectations(t)
		mockRequestValidator.AssertExpectations(t)
	})

	t.Run("InvalidRequest", func(testing *testing.T) {
		recorder := httptest.NewRecorder()
		getRequest := httptest.NewRequest(http.MethodGet, "http://someURL", nil)

		mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(false).Once()
		mockConversion.AssertNotCalled(t, "GetFlavorFormat", getRequest, vars, "attributes", "names", ",")

		ch.ServeHTTP(recorder, commonRequest)
		mockRequestValidator.AssertExpectations(t)
		mockConversion.AssertExpectations(t)
	})

	t.Run("ErrEncode", func(testing *testing.T) {
		mockEncoding.On("EncodeJSON", wdmpGet).Return([]byte(""), errors.New(errMsg)).Once()
		mockConversion.On("GetFlavorFormat", commonRequest, vars, "attributes", "names", ",").
			Return(wdmpGet, nil).Once()
		mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(true).Once()

		recorder := httptest.NewRecorder()
		ch.ServeHTTP(recorder, commonRequest)

		mockEncoding.AssertExpectations(t)
		mockConversion.AssertExpectations(t)
		mockRequestValidator.AssertExpectations(t)
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

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)
		origin := httptest.NewRecorder()

		ForwardHeadersByPrefix("H", origin, resp)
		assert.Empty(origin.Header())
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		origin := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("He", origin, resp)
		assert.NotEmpty(origin.Header())
		assert.EqualValues(1, len(origin.Header()))
		assert.EqualValues("3", origin.Header().Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)
		origin := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("H", origin, resp)
		assert.NotEmpty(origin.Header())
		assert.EqualValues(2, len(origin.Header()))
		assert.EqualValues([]string{"5", "6"}, origin.Header()["Hydrogen"])
	})
}

func TestHandleStat(t *testing.T) {
	ch := &ConversionHandler{sender: mockSender, logger: fakeLogger, targetURL: "http://targetURL.com", Requester: mockRequester}

	recorder := httptest.NewRecorder()
	emptyResponse := &http.Response{}

	req := httptest.NewRequest(http.MethodGet, "http://ThisMachineURL.com", nil)

	mockRequester.On("PerformRequest", mock.AnythingOfType("*http.Request")).Return(emptyResponse, nil).Once()
	mockSender.On("HandleResponse", ch, nil, emptyResponse, recorder, true).Once()
	mockSender.On("GetRespTimeout").Return(time.Second).Once()

	ch.HandleStat(recorder, req)
	mockSender.AssertExpectations(t)
	mockRequester.AssertExpectations(t)
}

func TestIsValidRequest(t *testing.T) {
	t.Run("NilURLVars", func(t *testing.T) {
		assert := assert.New(t)
		TR1RequestValidator := TR1RequestValidator{Logger: fakeLogger}
		assert.False(TR1RequestValidator.isValidRequest(nil, nil))
	})

	t.Run("InvalidService", func(t *testing.T) {
		assert := assert.New(t)
		TR1RequestValidator := TR1RequestValidator{Logger: fakeLogger}
		URLVars := map[string]string{"service": "wutService?"}
		origin := httptest.NewRecorder()
		assert.False(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusBadRequest, origin.Code)
	})

	t.Run("InvalidDeviceID", func(t *testing.T) {
		assert := assert.New(t)
		supportedServices := map[string]struct{}{"goodService": {}}
		TR1RequestValidator := TR1RequestValidator{Logger: fakeLogger, supportedServices: supportedServices}
		URLVars := map[string]string{"service": "goodService", "deviceid": "wutDevice?"}
		origin := httptest.NewRecorder()
		assert.False(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusBadRequest, origin.Code)
	})

	t.Run("IdealCase", func(t *testing.T) {
		assert := assert.New(t)
		supportedServices := map[string]struct{}{"goodService": {}}
		TR1RequestValidator := TR1RequestValidator{Logger: fakeLogger, supportedServices: supportedServices}
		URLVars := map[string]string{"service": "goodService", "deviceid": "mac:112233445566"}
		origin := httptest.NewRecorder()
		assert.True(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusOK, origin.Code) // check origin's statusCode hasn't been changed from default
	})
}

// Test Helpers //
func SetUpTest(encodeArg interface{}, req *http.Request) {
	recorder := httptest.NewRecorder()
	timeout := time.Nanosecond

	mockEncoding.On("EncodeJSON", encodeArg).Return(payload, nil).Once()
	mockSender.On("Send", ch, recorder, payload, mock.AnythingOfType("*http.Request")).Return(resp, nil).Once()
	mockSender.On("HandleResponse", ch, nil, resp, recorder, false).Once()
	mockSender.On("GetRespTimeout").Return(timeout).Once()
	mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(true).Once()

	ch.ServeHTTP(recorder, req)
}

func AssertCommonCalls(t *testing.T) {
	mockConversion.AssertExpectations(t)
	mockEncoding.AssertExpectations(t)
	mockSender.AssertExpectations(t)
	mockRequestValidator.AssertExpectations(t)
}

type LightFakeLogger struct{}

func (fake *LightFakeLogger) Log(_ ...interface{}) error {
	return nil
}
