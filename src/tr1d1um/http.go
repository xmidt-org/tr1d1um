package main

import (
	"bytes"
	"encoding/json"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"io"
	"io/ioutil"
	"net/http"
	"time"
)

type ConversionHandler struct {
	infoLogger  log.Logger
	errorLogger log.Logger
	timeOut     time.Duration
	targetUrl   string
	/*These functions should be set during handler set up */
	GetFlavorFormat     func(*http.Request, string, string, string) ([]byte, error)
	SetFlavorFormat     func(*http.Request, BodyReader) ([]byte, error)
	DeleteFlavorFormat  func(Vars, string) ([]byte, error)
	AddFlavorFormat     func(io.Reader, Vars, string, BodyReader) ([]byte, error)
	ReplaceFlavorFormat func(io.Reader, Vars, string, BodyReader) ([]byte, error)

	SendData func(*ConversionHandler, http.ResponseWriter, *wrp.SimpleRequestResponse)

	ReadAll BodyReader
}

func (sh *ConversionHandler) ConversionGETHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.GetFlavorFormat(req, "attributes", "names", ",")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	ConfigureWrp(wrp, req)

	sh.SendData(sh, resp, wrpMessage)
}

func (sh *ConversionHandler) ConversionSETHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.SetFlavorFormat(req, ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	ConfigureWrp(wrp, req)

	sh.SendData(sh, resp, wrpMessage)
}

func (sh *ConversionHandler) ConversionDELETEHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.DeleteFlavorFormat(mux.Vars(req), "parameter")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	ConfigureWrp(wrp, req)

	sh.SendData(sh, resp, wrpMessage)
}

func (sh *ConversionHandler) ConversionREPLACEHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.ReplaceFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	ConfigureWrp(wrp, req)

	sh.SendData(sh, resp, wrpMessage)
}

func (sh *ConversionHandler) ConversionADDHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.AddFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	ConfigureWrp(wrp, req)

	sh.SendData(sh, resp, wrpMessage)
}

func SendData(sh *ConversionHandler, resp http.ResponseWriter, wrpMessage *wrp.SimpleRequestResponse) {
	wrpPayload, err := json.Marshal(wrpMessage)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	clientWithDeadline := http.Client{Timeout: sh.timeOut}

	//todo: any headers to be added here
	requestToServer, err := http.NewRequest(http.MethodGet, sh.targetUrl ,bytes.NewBuffer(wrpPayload))
	requestToServer.Header.Set("Content-Type", wrp.JSON.ContentType())

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	respFromServer, err := clientWithDeadline.Do(requestToServer)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK { //something was not okay. Try to forward to original request
		resp.WriteHeader(respFromServer.StatusCode)
		if respBody, err := sh.ReadAll(respFromServer.Body); err != nil {
			resp.Write(respBody)
		}
		sh.errorLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	responsePayload, err := ExtractPayloadFromWrp(respFromServer.Body, ioutil.ReadAll)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	resp.WriteHeader(http.StatusOK)
	resp.Write(responsePayload)
}

func ConfigureWrp(wrpMsg * wrp.SimpleRequestResponse, req *http.Request){
	wrpMsg.ContentType = req.Header.Get("Content-Type")
	if pathVars := mux.Vars(req); pathVars != nil {
		deviceId, deviceIdExists := pathVars["deviceid"]
		service, serviceExists := pathVars["service"]
		if deviceIdExists && serviceExists {
			wrpMsg.Destination = deviceId + "/" + service
		}
	}
}

