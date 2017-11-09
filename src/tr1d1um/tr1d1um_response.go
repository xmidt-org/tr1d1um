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
	"encoding/json"
	"fmt"
	"net/http"

	"errors"

	"strings"

	"github.com/Comcast/webpa-common/wrp"
)

//Custom TR1 HTTP Status codes
const (
	Tr1StatusTimeout = 503
)

var unexpectedRKDResponse = errors.New("unexpectedRDKResponse")

//RDKResponse will serve as the struct to read in
//results coming from some RDK device
type RDKResponse struct {
	StatusCode int `json:"statusCode"`
}

type Tr1d1umResponse struct {
	Body    []byte
	Code    int
	Headers http.Header
	err     error
}

func (tr1Resp *Tr1d1umResponse) New() *Tr1d1umResponse {
	tr1Resp.Headers, tr1Resp.Body, tr1Resp.Code = http.Header{}, []byte{}, http.StatusOK
	return tr1Resp
}

//writeResponse is a tiny helper function that passes responses (In Json format only for now)
//to a caller
func writeResponse(message string, statusCode int, tr1Resp *Tr1d1umResponse) {
	tr1Resp.Headers.Set("Content-Type", wrp.JSON.ContentType())
	tr1Resp.Code = statusCode
	tr1Resp.Body = []byte(fmt.Sprintf(`{"message":"%s"}`, message))
}

//ShouldRetryOnError returns true if the given error is related to some timeout and such error is not being reporting back to
//some origin. Otherwise, it returns false. Note that such reporting is invoked in this function.
func ReportError(err error, tr1Resp *Tr1d1umResponse) {
	if err == nil {
		return
	}
	message, statusCode := "", http.StatusInternalServerError
	if err == context.Canceled || err == context.DeadlineExceeded ||
		strings.Contains(err.Error(), "Client.Timeout exceeded") {
		message, statusCode = "Error Timeout", Tr1StatusTimeout
	}

	writeResponse(message, statusCode, tr1Resp)

	return
}

//GetStatusCodeFromRDKResponse returns the status code given a well-formatted
//RDK response. Otherwise, it defaults to 500 as code and returns a pertinent error
func GetStatusCodeFromRDKResponse(RDKPayload []byte) (statusCode int, err error) {
	RDKResp := new(RDKResponse)
	statusCode = http.StatusInternalServerError
	if err = json.Unmarshal(RDKPayload, RDKResp); err == nil {
		if RDKResp.StatusCode != 0 { // some statusCode was actually provided
			statusCode = RDKResp.StatusCode
		} else {
			err = unexpectedRKDResponse
		}
	}
	return
}
