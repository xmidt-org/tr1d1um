package main

import (
	"net/http"
	"context"

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
		origin.WriteHeader(http.StatusInternalServerError)
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
	cancel()
	ch.sender.HandleResponse(ch, err, response, origin)
}
