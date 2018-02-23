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
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

const contentTypeKey = "Content-Type"

//ConversionHandler is the main arm of the operations supported by this server
type ConversionHandler struct {
	TargetURL     string
	WRPRequestURL string
	WdmpConvert   ConversionTool
	Sender        SendAndHandle
	RequestValidator
	RetryStrategy
	log.Logger
}

//ConversionHandler handles the different incoming tr1 requests
func (ch *ConversionHandler) ServeHTTP(origin http.ResponseWriter, req *http.Request) {
	requestArrivalTime := time.Now()
	var debugLogger, errorLogger = logging.Debug(ch), logging.Error(ch)

	debugLogger.Log(logging.MessageKey(), "ServeHTTP called")

	var (
		err     error
		wdmp    interface{}
		urlVars = mux.Vars(req)
	)

	if !ch.isValidRequest(urlVars, origin) {
		return
	}

	switch req.Method {
	case http.MethodGet:
		wdmp, err = ch.WdmpConvert.GetFlavorFormat(req, urlVars, "attributes", "names", ",")
		break

	case http.MethodPatch:
		wdmp, err = ch.WdmpConvert.SetFlavorFormat(req)
		break

	case http.MethodDelete:
		wdmp, err = ch.WdmpConvert.DeleteFlavorFormat(urlVars, "parameter")
		break

	case http.MethodPut:
		wdmp, err = ch.WdmpConvert.ReplaceFlavorFormat(req.Body, urlVars, "parameter")
		break

	case http.MethodPost:
		wdmp, err = ch.WdmpConvert.AddFlavorFormat(req.Body, urlVars, "parameter")
		break
	}

	if err != nil {
		WriteResponseWriter(err.Error(), http.StatusBadRequest, origin)
		logging.Error(ch).Log(logging.MessageKey(), ErrUnsuccessfulDataParse, logging.ErrorKey(), err.Error())
		return
	}

	wdmpPayload, err := json.Marshal(wdmp)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		logging.Error(ch).Log(logging.ErrorKey(), err.Error())
		return
	}

	wrpMsg := ch.WdmpConvert.GetConfiguredWRP(wdmpPayload, urlVars, req.Header)

	//Forward transaction id being used in Request
	origin.Header().Set(HeaderWPATID, wrpMsg.TransactionUUID)
	origin.Header().Set(contentTypeKey, wrp.JSON.ContentType())

	var wrpPayloadBuffer bytes.Buffer
	err = wrp.NewEncoder(&wrpPayloadBuffer, wrp.Msgpack).Encode(wrpMsg)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		logging.Error(ch).Log(logging.ErrorKey(), err)
		return
	}

	tr1Request := Tr1d1umRequest{
		method:  http.MethodPost,
		URL:     ch.WRPRequestURL,
		headers: http.Header{},
		body:    wrpPayloadBuffer.Bytes(),
	}

	tr1Request.headers.Set(contentTypeKey, wrp.Msgpack.ContentType())
	tr1Request.headers.Set("Authorization", req.Header.Get("Authorization"))

	tr1Resp, err := ch.Execute(req.Context(), ch.Sender.MakeRequest, tr1Request)

	if err != nil {
		errorLogger.Log(logging.MessageKey(), "error in retry execution", logging.ErrorKey(), err)
	}

	tr1d1umResp := tr1Resp.(*Tr1d1umResponse)

	bookkeepingLog(ch, tr1d1umResp, req, time.Now().Sub(requestArrivalTime), wrpMsg.TransactionUUID)
	TransferResponse(tr1d1umResp, origin)
}

//HandleStat handles the differentiated STAT command
func (ch *ConversionHandler) HandleStat(origin http.ResponseWriter, req *http.Request) {
	requestArrivalTime := time.Now()
	logging.Debug(ch).Log(logging.MessageKey(), "HandleStat called")
	var errorLogger = logging.Error(ch)

	tr1Request := Tr1d1umRequest{
		method:  http.MethodGet,
		URL:     ch.TargetURL + req.URL.RequestURI(),
		headers: http.Header{},
	}

	tr1Request.headers.Set("Authorization", req.Header.Get("Authorization"))

	tr1Resp, err := ch.Execute(req.Context(), ch.Sender.MakeRequest, tr1Request)

	if err != nil {
		errorLogger.Log(logging.MessageKey(), "error in retry execution", logging.ErrorKey(), err)
	}

	// we expect content to be of json format
	origin.Header().Set(contentTypeKey, wrp.JSON.ContentType())
	tr1d1umResp := tr1Resp.(*Tr1d1umResponse)

	bookkeepingLog(ch, tr1d1umResp, req, time.Now().Sub(requestArrivalTime), GetOrGenTID(req.Header))

	TransferResponse(tr1d1umResp, origin)
}

//RequestValidator verifies a request based the provided named URL variables
type RequestValidator interface {
	isValidRequest(map[string]string, http.ResponseWriter) bool
}

//TR1RequestValidator verifies the basic validity of incoming requests to XMIDT/WebPA
type TR1RequestValidator struct {
	supportedServices map[string]struct{}
	log.Logger
}

//isValid returns true if and only if both service and deviceID provided in the request are supported and valid respectively
func (validator *TR1RequestValidator) isValidRequest(URLVars map[string]string, origin http.ResponseWriter) (isValid bool) {
	if isValid = URLVars != nil; !isValid {
		return
	}

	//check request contains a valid service
	if _, isValid = validator.supportedServices[URLVars["service"]]; !isValid {
		WriteResponseWriter(fmt.Sprintf("Unsupported Service: %s", URLVars["service"]), http.StatusBadRequest, origin)
		logging.Error(validator).Log(logging.ErrorKey(), "unsupported service", "service", URLVars["service"])
		return
	}

	//check device id
	if _, err := device.ParseID(URLVars["deviceid"]); err != nil {
		WriteResponseWriter(fmt.Sprintf("Invalid devideID: %s", err.Error()), http.StatusBadRequest, origin)
		logging.Error(validator).Log(logging.ErrorKey(), err.Error(), logging.MessageKey(), "Invalid deviceID")
		return false
	}

	return
}

// Helper functions

//ForwardHeadersByPrefix forwards header values whose keys start with the given prefix from some response
//into an responseWriter
func ForwardHeadersByPrefix(prefix string, from *http.Response, to *Tr1d1umResponse) {
	if from == nil || from.Header == nil || to == nil || to.Headers == nil {
		return
	}
	for headerKey, headerValues := range from.Header {
		if strings.HasPrefix(headerKey, prefix) {
			for _, headerValue := range headerValues {
				to.Headers.Add(headerKey, headerValue)
			}
		}
	}
}

//ShouldRetryOnResponse determines whether or not to retry making another request
func ShouldRetryOnResponse(tr1Resp interface{}, _ error) (retry bool) {
	tr1Response := tr1Resp.(*Tr1d1umResponse)
	retry = tr1Response.Code == Tr1StatusTimeout
	return
}

//OnRetryInternalFailure defines the result values in case these cannot be generated due to
//some internal retry error
func OnRetryInternalFailure() (result interface{}) {
	tr1Resp := Tr1d1umResponse{}.New()
	tr1Resp.Code = http.StatusInternalServerError
	return tr1Resp
}

//TransferResponse simply dumps data from a Tr1d1umResponse to its http equivalent (ResponseWriter)
//This is needed due to the nature of retries. You can write multiple times to a tr1d1umResponse but
//only once to a ResponseWriter
func TransferResponse(from *Tr1d1umResponse, to http.ResponseWriter) {
	// Headers
	for headerKey, headerValues := range from.Headers {
		for _, headerValue := range headerValues {
			to.Header().Add(headerKey, headerValue)
		}
	}

	// Code
	to.WriteHeader(from.Code)

	// Body
	to.Write(from.Body)
}

//helper function that logs desired HTTP request/response info
func bookkeepingLog(logger log.Logger, tr1Resp *Tr1d1umResponse, req *http.Request, latency time.Duration, TID string) {
	logging.Info(logger).Log(
		logging.MessageKey(), "Bookkeeping response",
		"requestAddress", req.RemoteAddr,
		"requestURLPath", req.URL.Path,
		"requestMethod", req.Method,
		"responseHeaders", tr1Resp.Headers,
		"responseCode", tr1Resp.Code,
		"responseError", tr1Resp.err,
		"latency", latency,
		"tid", TID)
}
