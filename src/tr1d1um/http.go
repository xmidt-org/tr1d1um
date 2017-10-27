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
	"net/http"

	"fmt"
	"strings"

	"github.com/Comcast/webpa-common/logging"
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
}

//ConversionHandler handles the different incoming tr1 requests
func (ch *ConversionHandler) ServeHTTP(origin http.ResponseWriter, req *http.Request) {
	var (
		err     error
		wdmp    interface{}
		urlVars = mux.Vars(req)
	)

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
	response, err := ch.sender.Send(ch, origin, wdmpPayload, req.WithContext(ctx))
	cancel() // we are done using the context timeout on the request

	ForwardHeadersByPrefix("X", origin, response)

	ch.sender.HandleResponse(ch, err, response, origin)
}

// Helper functions

//ForwardHeadersByPrefix forwards header values whose keys start with the given prefix from some response
//into an responseWriter
func ForwardHeadersByPrefix(prefix string, origin http.ResponseWriter, resp *http.Response) {
	for headerKey, headerValues := range resp.Header {
		if strings.HasPrefix(headerKey, prefix) {
			for _, headerValue := range headerValues {
				origin.Header().Add(headerKey, headerValue)
			}
		}
	}
}
