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
	ConfigureRequest(context.Context, http.ResponseWriter, []byte) (*http.Request, error)
	HandleResponse(*ConversionHandler, error, *http.Response, http.ResponseWriter, bool, bool) bool
	GetRespTimeout() time.Duration
	GetWrpURL() string
	MakeRequest(requestArgs ...interface{}) (tr1Resp interface{}, err error)
}

//Tr1SendAndHandle implements the behaviors of SendAndHandle
type Tr1SendAndHandle struct {
	log            log.Logger
	NewHTTPRequest func(string, string, io.Reader) (*http.Request, error)
	respTimeout    time.Duration
	wrpURL         string
	Requester
	EncodingTool
}

type clientResponse struct {
	resp *http.Response
	err  error
}

type Tr1d1umRequest struct {
	ancestorCtx context.Context
	method      string
	URL         string
	body        []byte
	headers     http.Header
}

func (tr1Req *Tr1d1umRequest) GetBody() (body io.Reader) {
	if tr1Req != nil && tr1Req.body != nil {
		body = bytes.NewBuffer(tr1Req.body)
	}
	return
}

//func (tr1 *Tr1SendAndHandle) PrepareWRPMessag//`e
//Send prepares and subsequently sends a WRP encoded message to a predefined server
//Its response is then handled in HandleResponse
//todo: update description
func (tr1 *Tr1SendAndHandle) ConfigureRequest(ctx context.Context, tr1Response *Tr1d1umResponse, data io.Reader, method, URL string) (request *http.Request, err error) {

	return
}

//requestArgs = ctx, WRPData[],
//interface{}
func (tr1 *Tr1SendAndHandle) MakeRequest(requestArgs ...interface{}) (tr1Resp interface{}, err error) {
	tr1Request := requestArgs[0].(Tr1d1umRequest)
	newRequest, newRequestErr := tr1.NewHTTPRequest(tr1Request.method, tr1Request.URL, tr1Request.GetBody())

	tr1Response := Tr1d1umResponse{}.New()

	if newRequestErr != nil {
		tr1Response.Code = http.StatusInternalServerError
		err = newRequestErr
		return
	}

	ctx, cancel := context.WithTimeout(tr1Request.ancestorCtx, tr1.GetRespTimeout())
	defer cancel()

	newRequest = newRequest.WithContext(ctx)

	httpResp, err := tr1.PerformRequest(newRequest)
	tr1.HandleResponse(err, httpResp, tr1Response, tr1Request.method == http.MethodGet)
	tr1Resp = tr1Response
	return
}

//HandleResponse contains the instructions of what to write back to the original requester (origin)
//based on the responses of a server we have contacted through Send
func (tr1 *Tr1SendAndHandle) HandleResponse(err error, respFromServer *http.Response, tr1Resp *Tr1d1umResponse, wholeBody bool) {
	var errorLogger = logging.Error(tr1.log)
	var debugLogger = logging.Debug(tr1.log)

	if err != nil {
		tr1Resp.err = err
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK || wholeBody {
		tr1Resp.Body, tr1Resp.err = ioutil.ReadAll(respFromServer.Body)
		tr1Resp.Code = respFromServer.StatusCode
		debugLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	if RDKResponse, encodingErr := tr1.ExtractPayload(respFromServer.Body, wrp.JSON); encodingErr == nil {
		if RDKRespCode, RDKErr := GetStatusCodeFromRDKResponse(RDKResponse); RDKErr == nil && RDKRespCode != http.StatusInternalServerError {
			tr1Resp.Code = RDKRespCode
		}
		tr1Resp.Body = RDKResponse
	} else {
		tr1Resp.err = encodingErr
		errorLogger.Log(logging.ErrorKey(), err)
	}

	ForwardHeadersByPrefix("X", tr1Resp, respFromServer)
	return
}

//GetRespTimeout returns the duration the sender should use while waiting
//for a response from a server
func (tr1 *Tr1SendAndHandle) GetRespTimeout() time.Duration {
	return tr1.respTimeout
}

func (tr1 *Tr1SendAndHandle) GetWrpURL() string {
	return tr1.wrpURL
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

//forwardInput reads the given input into bytes and writes it to a given response struct
func forwardInput(tr1Resp *Tr1d1umResponse, input io.Reader) {
	if tr1Resp != nil && input != nil {
		if body, readErr := ioutil.ReadAll(input); readErr == nil {
			tr1Resp.Body = body
		} else {
			err = readErr
		}
	}
	return
}
