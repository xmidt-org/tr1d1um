package main

import (
	"context"
	"fmt"
	"net/http"

	"github.com/Comcast/webpa-common/wrp"
)

const (
	Tr1StatusTimeout = 503
)

type TR1Response struct {
	Message    string `json:"message,omitempty"`
	StatusCode int    `json:"code"`
}

//writeResponse is a tiny helper function that passes responses (In Json format only for now)
//to a caller
func writeResponse(message string, statusCode int, w http.ResponseWriter) {
	w.Header().Set("Content-Type", wrp.JSON.ContentType())
	w.WriteHeader(statusCode)
	w.Write([]byte(fmt.Sprintf(`{"message":"%s"}`, message)))
}

//ReportError writes back to the caller responses based on the given error
func ReportError(err error, w http.ResponseWriter) {
	message, statusCode := "", http.StatusInternalServerError
	if err == context.Canceled || err == context.DeadlineExceeded {
		message, statusCode = "Error Timeout", Tr1StatusTimeout
	}
	writeResponse(message, statusCode, w)
}
