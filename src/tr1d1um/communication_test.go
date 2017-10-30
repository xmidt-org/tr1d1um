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
	"context"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gopkg.in/h2non/gock.v1"
)

func TestSend(t *testing.T) {
	assert := assert.New(t)

	data := []byte("data")
	WRPMsg := &wrp.Message{}
	WRPPayload := []byte("payload")
	validURL := "http://someValidURL.com"

	ch := &ConversionHandler{encodingHelper: mockEncoding, wdmpConvert: mockConversion, targetURL: validURL,
		Requester: mockRequester, serverVersion: "v2"}

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
		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()
		mockRequester.On("PerformRequest", mock.AnythingOfType("*http.Request")).Return(&http.Response{
			StatusCode: http.StatusOK}, nil).Once()

		tr1 := NewTR1()

		resp, err := tr1.Send(ch, nil, data, req)

		assert.Nil(err)
		assert.EqualValues(http.StatusOK, resp.StatusCode)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
		mockRequester.AssertExpectations(t)
	})

	t.Run("SendTimeout", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWRP", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()
		mockRequester.On("PerformRequest", mock.AnythingOfType("*http.Request")).Return(&http.Response{}, context.DeadlineExceeded).Once()

		tr1 := NewTR1()

		_, err := tr1.Send(ch, nil, data, req)
		assert.EqualValues(context.DeadlineExceeded, err)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
		mockRequester.AssertExpectations(t)
	})
}

func TestHandleResponse(t *testing.T) {
	assert := assert.New(t)
	tr1 := &Tr1SendAndHandle{log: &LightFakeLogger{}}
	tr1.NewHTTPRequest = http.NewRequest

	ch := &ConversionHandler{encodingHelper: mockEncoding, wdmpConvert: mockConversion}

	t.Run("IncomingTimeoutErr", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(nil, context.DeadlineExceeded, nil, recorder, false)
		assert.EqualValues(Tr1StatusTimeout, recorder.Code)
	})

	t.Run("IncomingErr", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(nil, errors.New(errMsg), nil, recorder, false)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
	})

	t.Run("StatusNotOK", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		fakeResponse := &http.Response{StatusCode: http.StatusBadRequest}
		tr1.HandleResponse(nil, nil, fakeResponse, recorder, false)
		assert.EqualValues(http.StatusBadRequest, recorder.Code)
	})

	t.Run("ExtractPayloadFail", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.JSON).Return([]byte(""),
			errors.New(errMsg)).Once()
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("ExtractPayloadTimeout", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.JSON).Return([]byte(""),
			context.Canceled).Once()
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder, false)

		assert.EqualValues(Tr1StatusTimeout, recorder.Code)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("IdealCase", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		extractedData := []byte("extract")

		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.JSON).Return(extractedData, nil).Once()
		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(extractedData, recorder.Body.Bytes())
		mockEncoding.AssertExpectations(t)
	})

	t.Run("IdealReadEntireBody", func(t *testing.T) {
		var fakeBody bytes.Buffer
		bodyString := "bodyString"
		fakeBody.WriteString(bodyString)

		fakeResponse := &http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(&fakeBody)}

		recorder := httptest.NewRecorder()
		tr1.HandleResponse(ch, nil, fakeResponse, recorder, true)

		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(bodyString, recorder.Body.String())
		mockEncoding.AssertNotCalled(t, "ExtractPayload", fakeResponse.Body, wrp.JSON)
	})
}

func TestPerformRequest(t *testing.T) {
	testWaitGroup := &sync.WaitGroup{}
	testWaitGroup.Add(1)

	t.Run("RequestTimeout", func(t *testing.T) {
		defer testWaitGroup.Done()
		assert := assert.New(t)

		validURL := "http://someValidURL.com"
		req, _ := http.NewRequest(http.MethodGet, validURL, nil)
		ctx, cancel := context.WithCancel(req.Context())

		requester := &ContextTimeoutRequester{&http.Client{}}

		gock.New(validURL).Reply(http.StatusOK).Delay(time.Minute) // on purpose delaying response.

		wg := sync.WaitGroup{}
		wg.Add(1)

		errChan := make(chan error)

		go func() {
			wg.Done()
			_, err := requester.PerformRequest(req.WithContext(ctx))
			errChan <- err
		}()

		wg.Wait() //Wait until we know PerformRequest() is running
		cancel()

		assert.NotNil(<-errChan)
	})

	t.Run("RequestNoTimeout", func(t *testing.T) {
		testWaitGroup.Wait()
		gock.OffAll()

		assert := assert.New(t)

		requester := &ContextTimeoutRequester{&http.Client{}}

		someURL := "http://123.com"

		req, _ := http.NewRequest(http.MethodGet, someURL, nil)

		gock.New(someURL).Reply(http.StatusAccepted)

		resp, err := requester.PerformRequest(req)

		assert.Nil(err)
		assert.NotNil(resp)
		assert.EqualValues(http.StatusAccepted, resp.StatusCode)
	})
}

func TestForwardInput(t *testing.T) {

	t.Run("NilCases", func(t *testing.T) {
		assert := assert.New(t)
		recorder := httptest.NewRecorder()

		forwardInput(recorder, nil)
		assert.Empty(recorder.Body.Bytes())

		forwardInput(nil, nil)
		assert.Empty(recorder.Body.Bytes())
	})

	t.Run("NormalInput", func(t *testing.T) {
		assert := assert.New(t)

		var buf bytes.Buffer
		buf.WriteString("{")

		recorder := httptest.NewRecorder()

		forwardInput(recorder, &buf)
		assert.EqualValues([]byte("{"), recorder.Body.Bytes())
	})
}

func NewHTTPRequestFail(_, _ string, _ io.Reader) (*http.Request, error) {
	return nil, errors.New(errMsg)
}

func NewTR1() (tr1 *Tr1SendAndHandle) {
	tr1 = &Tr1SendAndHandle{log: &LightFakeLogger{}}
	tr1.NewHTTPRequest = http.NewRequest
	return tr1
}
