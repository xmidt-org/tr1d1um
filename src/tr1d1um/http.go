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
	"fmt"
	"net/http"
	"strings"

	"./retryUtilities"
	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

//ConversionHandler wraps the main WDMP -> WRP conversion method
type ConversionHandler struct {
	logger         log.Logger
	targetURL      string
	serverVersion  string
	wdmpConvert    ConversionTool
	sender         SendAndHandle
	encodingHelper EncodingTool
	RequestValidator
	retryUtilities.RetryStrategy
}

//ConversionHandler handles the different incoming tr1 requests
func (ch *ConversionHandler) ServeHTTP(origin http.ResponseWriter, req *http.Request) {
	var debugLogger = logging.Debug(ch.logger)
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
		wdmp, err = ch.wdmpConvert.GetFlavorFormat(req, urlVars, "attributes", "names", ",")
		break

	case http.MethodPatch:
		wdmp, err = ch.wdmpConvert.SetFlavorFormat(req)
		break

	case http.MethodDelete:
		wdmp, err = ch.wdmpConvert.DeleteFlavorFormat(urlVars, "parameter")
		break

	case http.MethodPut:
		wdmp, err = ch.wdmpConvert.ReplaceFlavorFormat(req.Body, urlVars, "parameter")
		break

	case http.MethodPost:
		wdmp, err = ch.wdmpConvert.AddFlavorFormat(req.Body, urlVars, "parameter")
		break
	}

	if err != nil {
		writeResponse(fmt.Sprintf("Error found during data parse: %s", err.Error()), http.StatusBadRequest, origin)
		logging.Error(ch.logger).Log(logging.MessageKey(), ErrUnsuccessfulDataParse, logging.ErrorKey(), err.Error())
		return
	}

	wdmpPayload, err := ch.encodingHelper.EncodeJSON(wdmp)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		logging.Error(ch.logger).Log(logging.ErrorKey(), err.Error())
		return
	}

	wrpMsg := ch.wdmpConvert.GetConfiguredWRP(wdmpPayload, urlVars, req.Header)

	//Forward transaction id being used in Request
	origin.Header().Set(HeaderWPATID, wrpMsg.TransactionUUID)

	wrpPayload, err := ch.encodingHelper.GenericEncode(wrpMsg, wrp.JSON)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		logging.Error(ch.logger).Log(logging.ErrorKey(), err)
		return
	}

	tr1Request := Tr1d1umRequest{
		ancestorCtx: req.Context(),
		method:      http.MethodPost,
		URL:         ch.sender.GetWrpURL(),
		headers:     http.Header{},
		body:        wrpPayload,
	}

	tr1Request.headers.Set("Content-Type", wrp.JSON.ContentType())
	tr1Request.headers.Set("Authorization", req.Header.Get("Authorization"))

	tr1Resp, err := ch.Execute(ch.sender.MakeRequest, tr1Request)
	//todo: tr1Resp contains all the final results we need to write back to our origin

}

//HandleStat handles the differentiated STAT command
func (ch *ConversionHandler) HandleStat(origin http.ResponseWriter, req *http.Request) {
	logging.Debug(ch.logger).Log(logging.MessageKey(), "HandleStat called")
	var errorLogger = logging.Error(ch.logger)

	tr1Request := Tr1d1umRequest{
		ancestorCtx: req.Context(),
		method:      http.MethodGet,
		URL:         ch.targetURL + req.URL.RequestURI(),
		headers:     http.Header{},
	}

	tr1Request.headers.Set("Authorization", req.Header.Get("Authorization"))

	tr1Resp, err := ch.Execute(ch.sender.MakeRequest, tr1Request)
	//todo:
}

type RequestValidator interface {
	isValidRequest(map[string]string, http.ResponseWriter) bool
}

type TR1RequestValidator struct {
	supportedServices map[string]struct{}
	log.Logger
}

//isValid returns true if and only if service is a supported one
func (validator *TR1RequestValidator) isValidRequest(URLVars map[string]string, origin http.ResponseWriter) (isValid bool) {
	if URLVars == nil {
		return false
	}

	//check request contains a valid service
	if _, isValid = validator.supportedServices[URLVars["service"]]; !isValid {
		writeResponse(fmt.Sprintf("Unsupported Service: %s", URLVars["service"]), http.StatusBadRequest, origin)
		logging.Error(validator).Log(logging.ErrorKey(), "unsupported service", "service", URLVars["service"])
		return
	}

	//check device id
	if _, err := device.ParseID(URLVars["deviceid"]); err != nil {
		writeResponse(fmt.Sprintf("Invalid devideID: %s", err.Error()), http.StatusBadRequest, origin)
		logging.Error(validator).Log(logging.ErrorKey(), err.Error(), logging.MessageKey(), "Invalid deviceID")
		return false
	}

	return
}

// Helper functions

//ForwardHeadersByPrefix forwards header values whose keys start with the given prefix from some response
//into an responseWriter
func ForwardHeadersByPrefix(prefix string, tr1Resp *Tr1d1umResponse, resp *http.Response) {
	if resp == nil || resp.Header == nil {
		return
	}

	for headerKey, headerValues := range resp.Header {
		if strings.HasPrefix(headerKey, prefix) {
			for _, headerValue := range headerValues {
				tr1Resp.Headers.Add(headerKey, headerValue)
			}
		}
	}
}
