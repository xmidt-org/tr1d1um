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

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

const errMsg = "shared failure"

var (
	payload, body              = []byte("SomePayload"), bytes.NewBufferString("body")
	resp                       = Tr1d1umResponse{}.New()
	mockConversion, mockSender = &MockConversionTool{}, &MockSendAndHandle{}
	mockRequestValidator       = &MockRequestValidator{}
	mockRetryStrategy          = &MockRetry{}
	ch                         = &ConversionHandler{
		WdmpConvert:      mockConversion,
		Sender:           mockSender,
		Logger:           logging.DefaultLogger(),
		RequestValidator: mockRequestValidator,
		RetryStrategy:    mockRetryStrategy,
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

	t.Run("DeleteInternalError", func(t *testing.T) {
		commonRequest = httptest.NewRequest(http.MethodDelete, commonURL, body)
		var urlVars Vars = mux.Vars(commonRequest)

		mockConversion.On("DeleteFlavorFormat", urlVars, "parameter").Return(wdmpDel, nil).Once()

		recorder := httptest.NewRecorder()

		wrpMsg := &wrp.Message{}
		mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(true).Once()
		mockConversion.On("GetConfiguredWRP", mock.AnythingOfType("[]uint8"), mock.Anything, commonRequest.Header).Return(wrpMsg).Once()
		mockRetryStrategy.On("Execute", commonRequest.Context(), mock.Anything, mock.Anything).Return(resp, errors.New("some internal "+
			"error")).Once()

		ch.ServeHTTP(recorder, commonRequest)
		AssertCommonCalls(t)
	})
}

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)

		to := Tr1d1umResponse{}.New()
		resp := &http.Response{Header: http.Header{}}

		ForwardHeadersByPrefix("H", resp, to)
		assert.Empty(to.Headers)
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		to := Tr1d1umResponse{}.New()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("He", resp, to)
		assert.NotEmpty(to.Headers)
		assert.EqualValues(1, len(to.Headers))
		assert.EqualValues("3", to.Headers.Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)
		to := Tr1d1umResponse{}.New()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("H", resp, to)
		assert.NotEmpty(to.Headers)
		assert.EqualValues(2, len(to.Headers))
		assert.EqualValues([]string{"5", "6"}, to.Headers["Hydrogen"])
	})

	t.Run("NilCases", func(t *testing.T) {
		to, resp := Tr1d1umResponse{}.New(), &http.Response{}
		//none of these should panic
		ForwardHeadersByPrefix("", nil, nil)
		ForwardHeadersByPrefix("", resp, to)
	})
}

func TestHandleStat(t *testing.T) {
	ch := &ConversionHandler{
		Sender:        mockSender,
		Logger:        logging.DefaultLogger(),
		TargetURL:     "http://targetURL.com",
		RetryStrategy: mockRetryStrategy,
	}

	t.Run("ExecuteNoError", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		req := httptest.NewRequest(http.MethodGet, "http://ThisMachineURL.com", nil)
		tr1Resp := Tr1d1umResponse{}.New()

		mockRetryStrategy.On("Execute", req.Context(), mock.Anything, mock.Anything).Return(tr1Resp, nil).Once()

		ch.HandleStat(recorder, req)
		mockRetryStrategy.AssertExpectations(t)
	})

	t.Run("ExecuteError", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		req := httptest.NewRequest(http.MethodGet, "http://ThisMachineURL.com", nil)
		tr1Resp := Tr1d1umResponse{}.New()

		mockRetryStrategy.On("Execute", req.Context(), mock.Anything, mock.Anything).Return(tr1Resp, errors.New("internal"+
			" error")).Once()

		ch.HandleStat(recorder, req)
		mockRetryStrategy.AssertExpectations(t)
	})
}

func TestIsValidRequest(t *testing.T) {
	t.Run("NilURLVars", func(t *testing.T) {
		assert := assert.New(t)
		TR1RequestValidator := TR1RequestValidator{Logger: logging.DefaultLogger()}
		assert.False(TR1RequestValidator.isValidRequest(nil, nil))
	})

	t.Run("InvalidService", func(t *testing.T) {
		assert := assert.New(t)
		TR1RequestValidator := TR1RequestValidator{Logger: logging.DefaultLogger()}
		URLVars := map[string]string{"service": "wutService?"}
		origin := httptest.NewRecorder()
		assert.False(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusBadRequest, origin.Code)
	})

	t.Run("InvalidDeviceID", func(t *testing.T) {
		assert := assert.New(t)
		supportedServices := map[string]struct{}{"goodService": {}}
		TR1RequestValidator := TR1RequestValidator{Logger: logging.DefaultLogger(), supportedServices: supportedServices}
		URLVars := map[string]string{"service": "goodService", "deviceid": "wutDevice?"}
		origin := httptest.NewRecorder()
		assert.False(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusBadRequest, origin.Code)
	})

	t.Run("IdealCase", func(t *testing.T) {
		assert := assert.New(t)
		supportedServices := map[string]struct{}{"goodService": {}}
		TR1RequestValidator := TR1RequestValidator{Logger: logging.DefaultLogger(), supportedServices: supportedServices}
		URLVars := map[string]string{"service": "goodService", "deviceid": "mac:112233445566"}
		origin := httptest.NewRecorder()
		assert.True(TR1RequestValidator.isValidRequest(URLVars, origin))
		assert.EqualValues(http.StatusOK, origin.Code) // check origin's statusCode hasn't been changed from default
	})
}

func TestOnRetryInternalFailure(t *testing.T) {
	assert := assert.New(t)
	result := OnRetryInternalFailure()
	tr1Resp := result.(*Tr1d1umResponse)

	assert.NotNil(tr1Resp.Headers)
	assert.EqualValues(http.StatusInternalServerError, tr1Resp.Code)
	assert.Empty(tr1Resp.Body)
}

func TestShouldRetryOnResponse(t *testing.T) {
	assert := assert.New(t)

	assert.False(ShouldRetryOnResponse(Tr1d1umResponse{}.New(), nil)) // statusOK case

	tr1Resp := Tr1d1umResponse{}.New()
	tr1Resp.Code = Tr1StatusTimeout
	assert.True(ShouldRetryOnResponse(tr1Resp, nil))
}

func TestTransferResponse(t *testing.T) {
	assert := assert.New(t)
	from, to := Tr1d1umResponse{}.New(), httptest.NewRecorder()

	from.Body = []byte("body")
	from.Headers.Add("k1", "v1")
	from.Headers.Add("k2", "v2")

	TransferResponse(from, to)
	assert.EqualValues(from.Body, to.Body.Bytes())
	assert.EqualValues(from.Code, to.Code)
	assert.EqualValues(to.Header().Get("k1"), "v1")
	assert.EqualValues(to.Header().Get("k2"), "v2")
}

// Test Helpers //
func SetUpTest(encodeArg interface{}, req *http.Request) {
	recorder := httptest.NewRecorder()

	wrpMsg := &wrp.Message{}
	mockRequestValidator.On("isValidRequest", mock.Anything, mock.Anything).Return(true).Once()
	mockConversion.On("GetConfiguredWRP", mock.AnythingOfType("[]uint8"), mock.Anything, req.Header).Return(wrpMsg).Once()
	mockRetryStrategy.On("Execute", req.Context(), mock.Anything, mock.Anything).Return(resp, nil).Once()

	ch.ServeHTTP(recorder, req)
}

func AssertCommonCalls(t *testing.T) {
	mockConversion.AssertExpectations(t)
	mockSender.AssertExpectations(t)
	mockRequestValidator.AssertExpectations(t)
	mockRetryStrategy.AssertExpectations(t)
}
