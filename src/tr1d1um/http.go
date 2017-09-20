package main

import (
	"bytes"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
	"github.com/gorilla/mux"
	"io"
	"net/http"
	"time"
)

type ConversionHandler struct {
	infoLogger  log.Logger
	errorLogger log.Logger
	timeOut     time.Duration
	targetUrl   string
	/*These functions should be set during handler set up */
	GetFlavorFormat     func(*http.Request, string, string, string) (*GetWDMP, error)
	SetFlavorFormat     func(*http.Request) (*SetWDMP, error)
	DeleteFlavorFormat  func(Vars, string) (*DeleteRowWDMP, error)
	AddFlavorFormat     func(io.Reader, Vars, string) (*AddRowWDMP, error)
	ReplaceFlavorFormat func(io.Reader, Vars, string) (*ReplaceRowsWDMP, error)

	SendRequest    func(*ConversionHandler, http.ResponseWriter, *wrp.Message)
	HandleResponse func(*ConversionHandler, http.ResponseWriter, *http.Request)
	EncodeJson     func(interface{}) ([]byte, error)
}

func (sh *ConversionHandler) ConversionHandler(resp http.ResponseWriter, req *http.Request) {
	var err error
	var wdmp interface{}

	switch req.Method {
	case http.MethodGet:
		wdmp, err = sh.GetFlavorFormat(req, "attributes", "names", ",")
		break

	case http.MethodPatch:
		wdmp, err = sh.SetFlavorFormat(req)
		break

	case http.MethodDelete:
		wdmp, err = sh.DeleteFlavorFormat(mux.Vars(req), "parameter")
		break

	case http.MethodPut:
		wdmp, err = sh.ReplaceFlavorFormat(req.Body, mux.Vars(req), "parameter")
		break

	case http.MethodPost:
		wdmp, err = sh.AddFlavorFormat(req.Body, mux.Vars(req), "parameter")
		break
	}

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wdmpPayload, err := sh.EncodeJson(wdmp)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err.Error())
		return
	}

	wrpMsg := GetConfiguredWrp(wdmpPayload, mux.Vars(req), req.Header)

	sh.SendRequest(sh, resp, wrpMsg)
}

func SendRequest(sh *ConversionHandler, resp http.ResponseWriter, wrpMessage *wrp.Message) {
	wrpPayload, err := GenericEncode(wrpMessage, wrp.JSON)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	requestToServer, err := http.NewRequest(http.MethodGet, sh.targetUrl, bytes.NewBuffer(wrpPayload))
	requestToServer.Header.Set("Content-Type", wrp.JSON.ContentType())
	//todo: any more headers to be added here

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}
	HandleResponse(sh, resp, requestToServer)
}

func HandleResponse(sh *ConversionHandler, resp http.ResponseWriter, req *http.Request) {
	clientWithDeadline := http.Client{Timeout: sh.timeOut}

	respFromServer, err := clientWithDeadline.Do(req)

	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
		return
	}

	if respFromServer.StatusCode != http.StatusOK {
		resp.WriteHeader(respFromServer.StatusCode)
		sh.errorLogger.Log(logging.MessageKey(), "non-200 response from server", logging.ErrorKey(), respFromServer.Status)
		return
	}

	if responsePayload, err := ExtractPayload(respFromServer.Body, wrp.JSON); err == nil {
		resp.WriteHeader(http.StatusOK)
		resp.Write(responsePayload)
	} else {
		resp.WriteHeader(http.StatusInternalServerError)
		sh.errorLogger.Log(logging.ErrorKey(), err)
	}
}

//Set the necessary fields in the wrp and return it
func GetConfiguredWrp(wdmp []byte, pathVars Vars, header http.Header) (wrpMsg *wrp.Message) {
	deviceId, _ := GetFromUrlPath("deviceid", pathVars)
	service, _ := GetFromUrlPath("service", pathVars)

	wrpMsg = &wrp.Message{
		Type:            wrp.SimpleRequestResponseMessageType,
		ContentType:     header.Get("Content-Type"),
		Payload:         wdmp,
		Source:          WRP_SOURCE + "/" + service,
		Destination:     deviceId + "/" + service,
		TransactionUUID: header.Get(HEADER_WPA_TID),
	}
	return
}
