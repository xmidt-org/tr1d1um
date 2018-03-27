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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/wrp"

	"github.com/Comcast/webpa-common/logging"
	"github.com/stretchr/testify/assert"
)

func TestMakeRequest(t *testing.T) {
	t.Run("BadNewRequest", func(t *testing.T) {
		assert := assert.New(t)
		tr1Req := Tr1d1umRequest{
			method:  "字", //make http.NewRequest fail with this awesome Chinese character.
			URL:     "http://someValidURL.com",
			headers: http.Header{},
			body:    []byte("d"),
		}

		tr1 := NewTR1()

		resp, err := tr1.MakeRequest(context.TODO(), tr1Req)
		assert.NotNil(resp)

		tr1Resp := resp.(*Tr1d1umResponse)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, tr1Resp.Code)
	})

	t.Run("RequestContextTimetout", func(t *testing.T) {
		assert := assert.New(t)

		slowServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			time.Sleep(time.Minute) // time out will for sure be triggered
		}))

		tr1Req := Tr1d1umRequest{
			method:  http.MethodGet,
			URL:     slowServer.URL,
			headers: http.Header{},
			body:    nil,
		}

		tr1 := NewTR1()

		ctx, cancel := context.WithCancel(context.TODO())

		go cancel() //fake an a quick timeout

		resp, err := tr1.MakeRequest(ctx, tr1Req)

		assert.NotNil(resp)
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "context canceled"))

		tr1Resp := resp.(*Tr1d1umResponse)
		assert.EqualValues(http.StatusServiceUnavailable, tr1Resp.Code)
	})

	t.Run("RequestContextNoTimetout", func(t *testing.T) {
		assert := assert.New(t)

		body := []byte(`aqua`)

		fastServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Write(body)
		}))

		tr1Req := Tr1d1umRequest{
			method:  http.MethodGet,
			URL:     fastServer.URL,
			headers: http.Header{},
			body:    nil,
		}

		tr1 := NewTR1()

		ctx, cancel := context.WithCancel(context.TODO())

		defer cancel()

		resp, err := tr1.MakeRequest(ctx, tr1Req)

		assert.NotNil(resp)
		assert.Nil(err)

		tr1Resp := resp.(*Tr1d1umResponse)

		assert.EqualValues(body, tr1Resp.Body)
		assert.EqualValues(http.StatusOK, tr1Resp.Code)
	})

}

func TestHandleResponse(t *testing.T) {
	tr1 := NewTR1()

	var testHeader = http.Header{}
	testHeader.Add("X-test", "test-val")

	t.Run("IncomingErr", func(t *testing.T) {
		assert := assert.New(t)
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(errors.New(errMsg), nil, recorder, false)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
	})

	t.Run("StatusNotOK", func(t *testing.T) {
		assert := assert.New(t)
		recorder := Tr1d1umResponse{}.New()

		fakeResponse := newTestingHTTPResponse(http.StatusBadRequest, "expectMe", testHeader)

		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusBadRequest, recorder.Code)
		assert.EqualValues("expectMe", string(recorder.Body))
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})

	t.Run("Scytale503s", func(t *testing.T) {
		assert := assert.New(t)
		recorder := Tr1d1umResponse{}.New()
		fakeResponse := newTestingHTTPResponse(http.StatusServiceUnavailable, "expectMe", testHeader)

		tr1.HandleResponse(nil, fakeResponse, recorder, false)
		assert.EqualValues(http.StatusServiceUnavailable, recorder.Code)
		assert.EqualValues("expectMe", string(recorder.Body))
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})

	t.Run("ExtractPayloadFail", func(t *testing.T) {
		assert := assert.New(t)
		fakeResponse := newTestingHTTPResponse(http.StatusOK, "", testHeader)
		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})

	t.Run("IdealReadEntireBody", func(t *testing.T) {
		assert := assert.New(t)
		fakeResponse := newTestingHTTPResponse(http.StatusOK, "read all of this", testHeader)

		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, true)

		assert.EqualValues(http.StatusOK, recorder.Code)
		assert.EqualValues("read all of this", string(recorder.Body))
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})

	t.Run("GoodRDKResponse", func(t *testing.T) {
		assert := assert.New(t)
		RDKResponse := []byte(`{"statusCode": 202}`)
		wrpMsg := wrp.Message{
			Type:    wrp.SimpleRequestResponseMessageType,
			Payload: RDKResponse}

		encodedData := wrp.MustEncode(wrpMsg, wrp.Msgpack)
		fakeResponse := newTestingHTTPResponse(http.StatusOK, string(encodedData), testHeader)

		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(202, recorder.Code)
		assert.EqualValues(RDKResponse, string(recorder.Body))
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})

	t.Run("IgnoredRDKResponseStatusCode", func(t *testing.T) {
		assert := assert.New(t)
		RDKResponse := []byte(`{"statusCode": 500}`) //status 500 is ignored to avoid ambiguities (server vs RDK device internal error)
		wrpMsg := wrp.Message{
			Type:    wrp.SimpleRequestResponseMessageType,
			Payload: RDKResponse}

		encodedData := wrp.MustEncode(wrpMsg, wrp.Msgpack)

		fakeResponse := newTestingHTTPResponse(http.StatusOK, string(encodedData), testHeader)

		recorder := Tr1d1umResponse{}.New()
		tr1.HandleResponse(nil, fakeResponse, recorder, false)

		assert.EqualValues(200, recorder.Code)
		assert.EqualValues(RDKResponse, string(recorder.Body))
		assert.True(bodyIsClosed(fakeResponse))
		assert.EqualValues(testHeader.Get("X-test"), recorder.Headers.Get("X-test"))
	})
}

func NewTR1() (tr1 *Tr1SendAndHandle) {
	tr1 = &Tr1SendAndHandle{
		Logger:      logging.DefaultLogger(),
		RespTimeout: time.Minute,
		client:      &http.Client{},
	}
	return tr1
}

//bodyCloseVerifier is a helper struct that helps track of whether or not some client called
//http.Response.Body.Close() after reading it.
type bodyCloseVerifier struct {
	io.Reader
	bodyClosed bool
}

func (b *bodyCloseVerifier) Close() (err error) {
	b.bodyClosed = true
	return
}

func newTestingHTTPResponse(code int, body string, header http.Header) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       &bodyCloseVerifier{bytes.NewBufferString(body), false},
		Header:     header,
	}
}

//bodyIsClosed is a simple helper that returns true if http.Response.Body.Close() was called.
//Note that correct results are only guaranteed if the body is an underlying bodyCloseVerifier
func bodyIsClosed(resp *http.Response) (isClosed bool) {
	if verifier, ofCorrectType := resp.Body.(*bodyCloseVerifier); ofCorrectType {
		isClosed = verifier.bodyClosed
	}
	return
}
