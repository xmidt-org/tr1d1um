package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"errors"

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

//writeResponse is a tiny helper function that passes responses (In Json format only for now)
//to a caller
func writeResponse(message string, statusCode int, w http.ResponseWriter) {
	w.Header().Set("Content-Type", wrp.JSON.ContentType())
	w.WriteHeader(statusCode)
	w.Write([]byte(fmt.Sprintf(`{"message":"%s"}`, message)))
}

//ReportError writes back to the caller responses based on the given error. Error must be non-nil to be reported.
func ReportError(err error, w http.ResponseWriter) {
	if err == nil {
		return
	}
	message, statusCode := "", http.StatusInternalServerError
	if err == context.Canceled || err == context.DeadlineExceeded {
		message, statusCode = "Error Timeout", Tr1StatusTimeout
	}
	writeResponse(message, statusCode, w)
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
