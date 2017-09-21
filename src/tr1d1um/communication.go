package main

import (
	"bytes"
	"net/http"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/gorilla/mux"
	"github.com/go-kit/kit/log"
	"io"
)

type SendAndHandle interface {
	Send(*ConversionHandler, http.ResponseWriter, []byte, *http.Request) (*http.Response, error)
	HandleResponse(*ConversionHandler, error, *http.Response, http.ResponseWriter)
}

type Tr1SendAndHandle struct {
	log log.Logger
	timedClient *http.Client 
	NewHTTPRequest func(string,string,io.Reader)(*http.Request,error)
}

func (tr1 *Tr1SendAndHandle) Send(ch *ConversionHandler, resp http.ResponseWriter, data []byte, req *http.Request) (respFromServer *http.Response, err error) {
	var errorLogger = logging.Error(tr1.log)
	wrpMsg := ch.wdmpConvert.GetConfiguredWrp(data, mux.Vars(req), req.Header)

	wrpPayload, err := ch.encodingHelper.GenericEncode(wrpMsg, wrp.JSON)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	requestToServer, err := tr1.NewHTTPRequest(http.MethodGet, ch.targetURL, bytes.NewBuffer(wrpPayload))

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	//todo: any more headers to be added here
	requestToServer.Header.Set("Content-Type", wrp.JSON.ContentType())
	respFromServer, err = tr1.timedClient.Do(requestToServer)
	return
}

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
		ch.errorLogger.Log(logging.ErrorKey(), err)
	}
}
