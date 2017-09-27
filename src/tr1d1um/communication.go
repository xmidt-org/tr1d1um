package main

import (
	"bytes"
	"io"
	"net/http"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

//SendAndHandle wraps the methods to communicate both back to a requester and to a target server
type SendAndHandle interface {
	Send(*ConversionHandler, http.ResponseWriter, []byte, *http.Request) (*http.Response, error)
	HandleResponse(*ConversionHandler, error, *http.Response, http.ResponseWriter)
}

//Tr1SendAndHandle implements the behaviors of SendAndHandle
type Tr1SendAndHandle struct {
	log            log.Logger
	timedClient    *http.Client
	NewHTTPRequest func(string, string, io.Reader) (*http.Request, error)
}

//Send prepares and subsequently sends a WRP encoded message to a predefined server
//Its response is then handled in HandleResponse
func (tr1 *Tr1SendAndHandle) Send(ch *ConversionHandler, resp http.ResponseWriter, data []byte, req *http.Request) (respFromServer *http.Response, err error) {
	var errorLogger = logging.Error(tr1.log)
	wrpMsg := ch.wdmpConvert.GetConfiguredWRP(data, mux.Vars(req), req.Header)

	wrpPayload, err := ch.encodingHelper.GenericEncode(wrpMsg, wrp.JSON)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	fullPath := ch.targetURL + baseURI + "/" + version + "/" + wrpMsg.Destination
	requestToServer, err := tr1.NewHTTPRequest(http.MethodPost, fullPath, bytes.NewBuffer(wrpPayload))

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	//todo: any more headers to be added here
	requestToServer.Header.Set("Content-Type", wrp.JSON.ContentType())
	requestToServer.Header.Set("Authorization", req.Header.Get("Authorization"))
	respFromServer, err = tr1.timedClient.Do(requestToServer)
	return
}

//HandleResponse contains the instructions of what to write back to the original requester (origin)
//based on the responses of a server we have contacted through Send
func (tr1 *Tr1SendAndHandle) HandleResponse(ch *ConversionHandler, err error, respFromServer *http.Response, origin http.ResponseWriter) {
	var errorLogger = logging.Error(tr1.log)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK {
		origin.WriteHeader(respFromServer.StatusCode)
		errorLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	if responsePayload, err := ch.encodingHelper.ExtractPayload(respFromServer.Body, wrp.JSON); err == nil {
		origin.WriteHeader(http.StatusOK)
		origin.Write(responsePayload)
	} else {
		origin.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
	}
}
