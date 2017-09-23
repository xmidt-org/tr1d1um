package main

import (
	"net/http"
	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	//	"github.com/Comcast/webpa-common/wrp"
	//	"encoding/json"
	//	"bytes"
	//	"fmt"
)

//ConversionHandler wraps the main WDMP -> WRP conversion method
type ConversionHandler struct {
	infoLogger     log.Logger
	errorLogger    log.Logger
	timeOut        time.Duration
	targetURL      string
	wdmpConvert    ConversionTool
	sender         SendAndHandle
	encodingHelper EncodingTool
}

//ConversionHandler handles the different incoming tr1 requests
func (ch *ConversionHandler) ServeHTTP(origin http.ResponseWriter, req *http.Request) {
	var err error
	var wdmp interface{}
	var urlVars Vars

	switch req.Method {
	case http.MethodGet:
		wdmp, err = ch.wdmpConvert.GetFlavorFormat(req, "attributes", "names", ",")
		break

	case http.MethodPatch:
		wdmp, err = ch.wdmpConvert.SetFlavorFormat(req)
		break

	case http.MethodDelete:
		urlVars = mux.Vars(req)
		wdmp, err = ch.wdmpConvert.DeleteFlavorFormat(urlVars, "parameter")
		break

	case http.MethodPut:
		urlVars = mux.Vars(req)
		wdmp, err = ch.wdmpConvert.ReplaceFlavorFormat(req.Body, urlVars, "parameter")
		break

	case http.MethodPost:
		urlVars = mux.Vars(req)
		wdmp, err = ch.wdmpConvert.AddFlavorFormat(req.Body, urlVars, "parameter")
		break
	}

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		ch.errorLogger.Log(logging.MessageKey(), ErrUnsuccessfulDataParse, logging.ErrorKey(), err.Error())
		return
	}

	wdmpPayload, err := ch.encodingHelper.EncodeJSON(wdmp)

	if err != nil {
		origin.WriteHeader(http.StatusInternalServerError)
		ch.errorLogger.Log(logging.ErrorKey(), err.Error())
		return
	}

	response, err := ch.sender.Send(ch, origin, wdmpPayload, req)
	ch.sender.HandleResponse(ch, err, response, origin)
}
