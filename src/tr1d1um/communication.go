package main

import (
	"bytes"
	"net/http"

	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
)

type SendAndHandle interface {
	Send(*ConversionHandler, http.ResponseWriter, []byte, *http.Request) (*http.Response, error)
	HandleResponse(*ConversionHandler, error, *http.Response, http.ResponseWriter)
}

type Tr1SendAndHandle struct {
	errorLogger log.Logger
}

func (tr1 *Tr1SendAndHandle) Send(ch *ConversionHandler, resp http.ResponseWriter, data []byte, req *http.Request) (respFromServer *http.Response, err error) {
	wrpMsg := ch.wdmpConvert.GetConfiguredWrp(data, mux.Vars(req), req.Header)

	wrpPayload, err := ch.encodingHelper.GenericEncode(wrpMsg, wrp.JSON)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		ch.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	requestToServer, err := http.NewRequest(http.MethodGet, ch.targetURL, bytes.NewBuffer(wrpPayload))
	requestToServer.Header.Set("Content-Type", wrp.JSON.ContentType())
	//todo: any more headers to be added here

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		ch.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	clientWithDeadline := http.Client{Timeout: ch.timeOut}

	respFromServer, err = clientWithDeadline.Do(requestToServer)
	return
}

func (tr1 *Tr1SendAndHandle) HandleResponse(ch *ConversionHandler, err error, respFromServer *http.Response, origin http.ResponseWriter) {
	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		tr1.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK {
		origin.WriteHeader(respFromServer.StatusCode)
		tr1.errorLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
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
