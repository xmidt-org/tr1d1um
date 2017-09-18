package main

import (
	"bytes"
	"encoding/json"
	"errors"
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

	SendData func(time.Duration, http.ResponseWriter, *wrp.SimpleRequestResponse)
}

func (sh ConversionHandler) ConversionGETHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.GetFlavorFormat(req, "attributes", "names", ",")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	sh.SendData(sh.timeOut, resp, wrpMessage)
}

func (sh ConversionHandler) ConversionSETHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.SetFlavorFormat(req, ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	sh.SendData(sh.timeOut, resp, wrpMessage)
}

func (sh ConversionHandler) ConversionDELETEHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.DeleteFlavorFormat(mux.Vars(req), "parameter")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	sh.SendData(sh.timeOut, resp, wrpMessage)
}

func (sh ConversionHandler) ConversionREPLACEHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.ReplaceFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	sh.SendData(sh.timeOut, resp, wrpMessage)
}

func (sh ConversionHandler) ConversionADDHandler(resp http.ResponseWriter, req *http.Request) {
	wdmpPayload, err := sh.AddFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType, Payload: wdmpPayload}

	sh.SendData(sh.timeOut, resp, wrpMessage)
}

func SendData(timeOut time.Duration, resp http.ResponseWriter, wrpMessage *wrp.SimpleRequestResponse) {
	//todo: some work needs to happen here like setting the destination of the device, etc.
	wrpPayload, err := json.Marshal(wrpMessage)

	if err != nil {
		err = errors.New("unsuccessful wrp conversion to json")
		return
	}

	clientWithDeadline := http.Client{Timeout: timeOut}

	//todo: any headers to be added here
	requestToServer, err := http.NewRequest("GET", "someTargetUrl", bytes.NewBuffer(wrpPayload))
	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte("Error creating new request"))
		return
	}

	respFromServer, err := clientWithDeadline.Do(requestToServer)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte("Error while posting request"))
		return
	}

	//Try forwarding back the response to the initial requester
	resp.WriteHeader(respFromServer.StatusCode)
	resp.Write([]byte(respFromServer.Status))
}
