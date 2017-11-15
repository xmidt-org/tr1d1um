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
	"io/ioutil"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"gopkg.in/h2non/gock.v1"
)

var (
	mockRequester = &MockRequester{}
)

func TestMakeRequest(t *testing.T) {
	validURL := "http://someValidURL.com"

	t.Run("BadNewRequest", func(t *testing.T) {
		assert := assert.New(t)
		tr1Req := Tr1d1umRequest{
			ancestorCtx: context.TODO(),
			method:      "字", //make http.NewRequest fail with this awesome Chinese character.
			URL:         validURL,
			headers:     http.Header{},
			body:        []byte("d"),
		}

		tr1 := NewTR1()

		resp, err := tr1.MakeRequest(tr1Req)
		assert.NotNil(resp)

		tr1Resp := resp.(*Tr1d1umResponse)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, tr1Resp.Code)
	})

	t.Run("InternalError", func(t *testing.T) {
		assert := assert.New(t)
		tr1Req := Tr1d1umRequest{
			ancestorCtx: context.TODO(),
			method:      "GET",
			URL:         validURL,
			headers:     http.Header{"key": []string{"value"}},
		}

		tr1 := NewTR1()

		someErr := errors.New("something went wrong")
		mockRequester.On("PerformRequest", mock.AnythingOfType("*http.Request")).Return(&http.Response{},
			someErr).Once()

		resp, err := tr1.MakeRequest(tr1Req)
		assert.NotNil(resp)

		tr1Resp := resp.(*Tr1d1umResponse)

		assert.EqualValues(someErr, err)
		assert.EqualValues(http.StatusInternalServerError, tr1Resp.Code)

		mockRequester.AssertExpectations(t)
	})
}

func TestHandleResponse(t *testing.T) {
	assert := assert.New(t)
	tr1 := NewTR1()

	t.Run("IncomingTimeoutErr", func(t *testing.T) {
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(context.DeadlineExceeded, nil, recorder, false)
		assert.EqualValues(Tr1StatusTimeout, recorder.Code)
	})

	t.Run("IncomingErr", func(t *testing.T) {
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(errors.New(errMsg), nil, recorder, false)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
	})

	t.Run("StatusNotOK", func(t *testing.T) {
		recorder := Tr1d1umResponse{}.New()
		var mockBody bytes.Buffer
		mockBody.WriteString("expectMe")
		fakeResponse := &http.Response{StatusCode: http.StatusBadRequest, Body: ioutil.NopCloser(&mockBody)}

		tr1.HandleResponse(nil, fakeResponse, recorder, false)
		assert.EqualValues(http.StatusBadRequest, recorder.Code)
		assert.EqualValues("expectMe", string(recorder.Body))
	})


	t.Run("503Into504", func(t *testing.T) {
		recorder := Tr1d1umResponse{}.New()
		var mockBody bytes.Buffer
		mockBody.WriteString("expectMe")
		fakeResponse := &http.Response{StatusCode: http.StatusServiceUnavailable, Body: ioutil.NopCloser(&mockBody)}

		tr1.HandleResponse(nil, fakeResponse, recorder, false)
		assert.EqualValues(http.StatusGatewayTimeout, recorder.Code)
		assert.EqualValues("expectMe", string(recorder.Body))
	})

	t.Run("ExtractPayloadFail", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.Msgpack).Return([]byte(""),
			errors.New(errMsg)).Once()
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("ExtractPayloadTimeout", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.Msgpack).Return([]byte(""),
			context.Canceled).Once()
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(Tr1StatusTimeout, recorder.Code)
		mockEncoding.AssertExpectations(t)
	})

	t.Run("IdealReadEntireBody", func(t *testing.T) {
		var fakeBody bytes.Buffer
		bodyString := "bodyString"
		fakeBody.WriteString(bodyString)

		fakeResponse := &http.Response{StatusCode: http.StatusOK, Body: ioutil.NopCloser(&fakeBody)}

		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, true)

		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues(bodyString, string(recorder.Body))
		mockEncoding.AssertNotCalled(t, "ExtractPayload", fakeResponse.Body, wrp.Msgpack)
	})

	t.Run("GoodRDKResponse", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		extractedData := []byte(`{"statusCode": 202}`)

		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.Msgpack).Return(extractedData, nil).Once()
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(202, recorder.Code)
		assert.EqualValues(extractedData, string(recorder.Body))
		mockEncoding.AssertExpectations(t)
	})

	t.Run("BadRDKResponse", func(t *testing.T) {
		fakeResponse := &http.Response{StatusCode: http.StatusOK}
		extractedData := []byte(`{"statusCode": 500}`)

		mockEncoding.On("ExtractPayload", fakeResponse.Body, wrp.Msgpack).Return(extractedData, nil).Once()
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusOK, recorder.Code) // reflect transaction instead of device status
		assert.EqualValues(extractedData, string(recorder.Body))
		mockEncoding.AssertExpectations(t)
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

func NewTR1() (tr1 *Tr1SendAndHandle) {
	tr1 = &Tr1SendAndHandle{
		Logger:       logging.DefaultLogger(),
		Requester:    mockRequester,
		EncodingTool: mockEncoding,
		RespTimeout:  time.Minute,
	}
	return tr1
}
