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
	MakeRequest(requestArgs ...interface{}) (tr1Resp interface{}, err error)
	HandleResponse(error, *http.Response, *Tr1d1umResponse, bool)
	GetRespTimeout() time.Duration
}

//SendAndHandleFactory serves as a fool-proof (you must provide all needed values for struct) initializer for SendAndHandle
type SendAndHandleFactory struct{}

//New does the initialization of a SendAndHandle (of actual type Tr1SendAndHandle)
func (factory SendAndHandleFactory) New(respTimeout time.Duration, requester Requester, encoding EncodingTool,
	logger log.Logger) SendAndHandle {
	return &Tr1SendAndHandle{
		RespTimeout:  respTimeout,
		Requester:    requester,
		EncodingTool: encoding,
		Logger:       logger,
	}
}

//Tr1SendAndHandle provides one implementation of SendAndHandle
type Tr1SendAndHandle struct {
	RespTimeout time.Duration
	Requester
	EncodingTool
	log.Logger
}

type clientResponse struct {
	resp *http.Response
	err  error
}

//Tr1d1umRequest provides a clean way to store information needed to make some request (in our case, it is http but it is not
// limited to that).
type Tr1d1umRequest struct {
	ancestorCtx context.Context
	method      string
	URL         string
	body        []byte
	headers     http.Header
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
func (tr1 *Tr1SendAndHandle) MakeRequest(requestArgs ...interface{}) (interface{}, error) {
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

	ctx, cancel := context.WithTimeout(tr1Request.ancestorCtx, tr1.GetRespTimeout())
	defer cancel()

	newRequest = newRequest.WithContext(ctx)

	httpResp, responseErr := tr1.PerformRequest(newRequest)
	tr1.HandleResponse(responseErr, httpResp, tr1Response, tr1Request.method == http.MethodGet)
	return tr1Response, responseErr
}

//HandleResponse contains the logic to generate a tr1d1umResponse based on some given error information and an http response
func (tr1 *Tr1SendAndHandle) HandleResponse(err error, respFromServer *http.Response, tr1Resp *Tr1d1umResponse, wholeBody bool) {
	var errorLogger = logging.Error(tr1)
	var debugLogger = logging.Debug(tr1)

	if err != nil {
		ReportError(err, tr1Resp)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK || wholeBody {
		tr1Resp.Body, tr1Resp.err = ioutil.ReadAll(respFromServer.Body)
		tr1Resp.Code = respFromServer.StatusCode
		ReportError(tr1Resp.err, tr1Resp)
		debugLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	if RDKResponse, encodingErr := tr1.ExtractPayload(respFromServer.Body, wrp.JSON); encodingErr == nil {
		if RDKRespCode, RDKErr := GetStatusCodeFromRDKResponse(RDKResponse); RDKErr == nil && RDKRespCode != http.StatusInternalServerError {
			tr1Resp.Code = RDKRespCode
		}
		tr1Resp.Body = RDKResponse
	} else {
		ReportError(encodingErr, tr1Resp)
		errorLogger.Log(logging.ErrorKey(), err)
	}

	ForwardHeadersByPrefix("X", respFromServer, tr1Resp)
	return
}

//GetRespTimeout returns the duration the sender should use while waiting
//for a response from a server
func (tr1 *Tr1SendAndHandle) GetRespTimeout() time.Duration {
	return tr1.RespTimeout
}

//Requester has the main functionality of taking a request, performing such request based on some internal client and
// simply returning the response and potential err when applicable
type Requester interface {
	PerformRequest(*http.Request) (*http.Response, error)
}

//ContextTimeoutRequester is a Requester realization that executes an http request respecting any context deadlines (or
// cancellations)
type ContextTimeoutRequester struct {
	client *http.Client
}

//PerformRequest makes its client execute the request asynchronously and guarantees that the cancellations or
// timeouts of the request's context is respected
func (c *ContextTimeoutRequester) PerformRequest(request *http.Request) (resp *http.Response, err error) {
	responseReady := make(chan clientResponse)
	ctx := request.Context()

	go func() {
		defer close(responseReady)
		respObj, respErr := c.client.Do(request)
		responseReady <- clientResponse{respObj, respErr}
	}()

	select {
	case cResponse := <-responseReady:
		resp, err = cResponse.resp, cResponse.err
		return
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
