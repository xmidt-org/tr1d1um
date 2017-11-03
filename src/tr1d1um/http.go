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
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/Comcast/webpa-common/logging"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"github.com/Comcast/webpa-common/device"
)

//ConversionHandler wraps the main WDMP -> WRP conversion method
type ConversionHandler struct {
	logger         log.Logger
	targetURL      string
	serverVersion  string
	wdmpConvert    ConversionTool
	sender         SendAndHandle
	encodingHelper EncodingTool
	Requester
	RequestValidator
}

//ConversionHandler handles the different incoming tr1 requests
func (ch *ConversionHandler) ServeHTTP(origin http.ResponseWriter, req *http.Request) {
	logging.Debug(ch.logger).Log(logging.MessageKey(), "ServeHTTP called")

	var (
		err     error
		wdmp    interface{}
		urlVars = mux.Vars(req)
	)

	if !ch.isValidRequest(req, origin){
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

	//set timeout for response waiting
	ctx, cancel := context.WithTimeout(req.Context(), ch.sender.GetRespTimeout())
	defer cancel()

	response, err := ch.sender.Send(ch, origin, wdmpPayload, req.WithContext(ctx))
	ch.sender.HandleResponse(ch, err, response, origin, false)
}

//HandleStat handles the differentiated STAT command
func (ch *ConversionHandler) HandleStat(origin http.ResponseWriter, req *http.Request) {
	logging.Debug(ch.logger).Log(logging.MessageKey(), "HandleStat called")
	var errorLogger = logging.Error(ch.logger)

	ctx, cancel := context.WithTimeout(req.Context(), ch.sender.GetRespTimeout())
	defer cancel()

	fullPath := ch.targetURL + req.URL.RequestURI()
	requestToServer, err := http.NewRequest(http.MethodGet, fullPath, nil)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	requestToServer.Header.Set("Authorization", req.Header.Get("Authorization"))
	requestWithContext := requestToServer.WithContext(ctx)

	response, err := ch.PerformRequest(requestWithContext)

	origin.Header().Set("Content-Type", "application/json")
	ch.sender.HandleResponse(ch, err, response, origin, true)
}

type RequestValidator interface {
	isValidRequest(*http.Request, http.ResponseWriter) bool
	isValidService(string) bool
}

type TR1RequestValidator struct {
	supportedServices map[string]struct{}
	log.Logger
}

//isValid returns true if and only if service is a supported one
func (validator *TR1RequestValidator) isValidRequest(req *http.Request, origin http.ResponseWriter) (isValid bool) {
	URLVars := mux.Vars(req)

	//check request contains a valid service
	if isValid = validator.isValidService(URLVars["service"]); !isValid {
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
func (validator *TR1RequestValidator) isValidService(service string) (isValid bool) {
	_, isValid = validator.supportedServices[service]
	return
}

// Helper functions

//ForwardHeadersByPrefix forwards header values whose keys start with the given prefix from some response
//into an responseWriter
func ForwardHeadersByPrefix(prefix string, origin http.ResponseWriter, resp *http.Response) {
	if resp == nil || resp.Header == nil {
		return
	}

	for headerKey, headerValues := range resp.Header {
		if strings.HasPrefix(headerKey, prefix) {
			for _, headerValue := range headerValues {
				origin.Header().Add(headerKey, headerValue)
			}
		}
	}
}

