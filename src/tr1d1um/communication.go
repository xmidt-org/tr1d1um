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
	"io"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
)

//SendAndHandle wraps the methods to communicate both back to a requester and to a target server
type SendAndHandle interface {
	MakeRequest(context.Context, ...interface{}) (tr1Resp interface{}, err error)
	HandleResponse(error, *http.Response, *Tr1d1umResponse, bool)
	GetRespTimeout() time.Duration
}

//Tr1SendAndHandle provides one implementation of SendAndHandle
type Tr1SendAndHandle struct {
	RespTimeout time.Duration
	log.Logger
	client *http.Client
}

//Tr1d1umRequest provides a clean way to store information needed to make some request (in our case, it is http but it is not
// limited to that).
type Tr1d1umRequest struct {
	method  string
	URL     string
	body    []byte
	headers http.Header
}

//GetBody is a handy function to provide the payload (body) of Tr1d1umRequest as a fresh reader
func (tr1Req *Tr1d1umRequest) GetBody() (body io.Reader) {
	if tr1Req != nil && tr1Req.body != nil {
		body = bytes.NewBuffer(tr1Req.body)
	}
	return
}

//MakeRequest contains all the logic that actually performs an http request
//It is tightly coupled with HandleResponse
func (tr1 *Tr1SendAndHandle) MakeRequest(ctx context.Context, requestArgs ...interface{}) (interface{}, error) {
	tr1Request := requestArgs[0].(Tr1d1umRequest)
	newRequest, newRequestErr := http.NewRequest(tr1Request.method, tr1Request.URL, tr1Request.GetBody())
	tr1Response := Tr1d1umResponse{}.New()

	if newRequestErr != nil {
		tr1Response.Code = http.StatusInternalServerError
		return tr1Response, newRequestErr
	}

	//transfer headers to request
	for headerKey := range tr1Request.headers {
		for _, headerValue := range tr1Request.headers[headerKey] {
			newRequest.Header.Add(headerKey, headerValue)
		}
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, tr1.GetRespTimeout())
	defer cancel()

	httpResp, responseErr := tr1.client.Do(newRequest.WithContext(timeoutCtx))
	tr1.HandleResponse(responseErr, httpResp, tr1Response, tr1Request.method == http.MethodGet)
	return tr1Response, responseErr
}

//HandleResponse contains the logic to generate a tr1d1umResponse based on some given error information and an http response
func (tr1 *Tr1SendAndHandle) HandleResponse(err error, respFromServer *http.Response, tr1Resp *Tr1d1umResponse, wholeBody bool) {
	var errorLogger = logging.Error(tr1)
	var debugLogger = logging.Debug(tr1)

	if err != nil {
		ReportError(err, tr1Resp)
		errorLogger.Log(logging.MessageKey(), "got an error instead of an http.Response", logging.ErrorKey(), err)
		return
	}

	//as a client, we are responsible to close the body after it gets read below
	defer respFromServer.Body.Close()
	bodyBytes, errReading := ioutil.ReadAll(respFromServer.Body)

	if errReading != nil {
		ReportError(errReading, tr1Resp)
		errorLogger.Log(logging.MessageKey(), "error reading http.Response body", logging.ErrorKey(), errReading)
		return
	}

	if respFromServer.StatusCode != http.StatusOK || wholeBody {
		tr1Resp.Body, tr1Resp.Code = bodyBytes, respFromServer.StatusCode

		debugLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	ResponseData := &wrp.Message{Type: wrp.SimpleRequestResponseMessageType}

	if errDecoding := wrp.NewDecoder(bytes.NewBuffer(bodyBytes), wrp.Msgpack).Decode(ResponseData); errDecoding == nil {
		RDKResponse := ResponseData.Payload

		if RDKRespCode, RDKErr := GetStatusCodeFromRDKResponse(RDKResponse); RDKErr == nil && RDKRespCode != http.StatusInternalServerError {
			tr1Resp.Code = RDKRespCode
		}

		tr1Resp.Body = RDKResponse
	} else {
		ReportError(errDecoding, tr1Resp)
		errorLogger.Log(logging.MessageKey(), "could not extract payload from wrp body", logging.ErrorKey(), errDecoding)
	}

	ForwardHeadersByPrefix("X", respFromServer, tr1Resp)
	return
}

//GetRespTimeout returns the duration the sender should use while waiting
//for a response from a server
func (tr1 *Tr1SendAndHandle) GetRespTimeout() time.Duration {
	return tr1.RespTimeout
}
