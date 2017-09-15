package main
import (
	"net/http"
	"time"
	"bytes"
	"github.com/go-kit/kit/log"
	"github.com/Comcast/webpa-common/logging"
	"io/ioutil"
	"github.com/Comcast/webpa-common/wrp"
	"encoding/json"
	"errors"
	"io"
	"github.com/gorilla/mux"
)
type ConversionHandler struct {
	infoLogger log.Logger
	errorLogger log.Logger
	timeOut time.Duration
	targetUrl string
	/*These functions should be set during handler set up */
	GetFlavorFormat func(*http.Request, string, string, string) ([]byte, error)
	SetFlavorFormat func(*http.Request, BodyReader) ([]byte, error)
	DeleteFlavorFormat func(Vars, string) ([]byte, error)
	AddFlavorFormat func(io.Reader, Vars, string, BodyReader) ([]byte, error)
	ReplaceFlavorFormat func(io.Reader, Vars, string, BodyReader) ([]byte, error)
}

func (sh ConversionHandler) ConversionGETHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.GetFlavorFormat(req, "attributes", "names", ",")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpPayload}

	sh.SendData(resp, wrpMessage)
}

func (sh ConversionHandler) ConversionSETHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.SetFlavorFormat(req, ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpPayload}

	sh.SendData(resp, wrpMessage)
}

func (sh ConversionHandler) ConversionDELETEHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.DeleteFlavorFormat(mux.Vars(req), "parameter")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpPayload}

	sh.SendData(resp, wrpMessage)
}

func (sh ConversionHandler) ConversionREPLACEHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.ReplaceFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpPayload}

	sh.SendData(resp, wrpMessage)
}

func (sh ConversionHandler) ConversionADDHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.AddFlavorFormat(req.Body, mux.Vars(req), "parameter", ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpMessage := &wrp.SimpleRequestResponse{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpPayload}

	sh.SendData(resp, wrpMessage)
}

func (sh ConversionHandler) SendData(resp http.ResponseWriter, wrpMessage *wrp.SimpleRequestResponse){
	wrpPayload, err := json.Marshal(wrpMessage)

	if err != nil {
		err = errors.New("unsuccessful wrp conversion to json")
		return
	}

	clientWithDeadline := http.Client{Timeout:sh.timeOut}

	//todo: any headers to be added here
	requestToServer, err := http.NewRequest("GET", sh.targetUrl, bytes.NewBuffer(wrpPayload))
	if err != nil {
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte("Error creating new request"))
		sh.errorLogger.Log(logging.MessageKey(), "Could not create new request", logging.ErrorKey(), err.Error())
		return
	}

	respFromServer, err := clientWithDeadline.Do(requestToServer)

	if err != nil{
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte("Error while posting request"))
		sh.errorLogger.Log(logging.MessageKey(), "Could not complete request", logging.ErrorKey(), err.Error())
		return
	}

	//Try forwarding back the response to the initial requester
	resp.WriteHeader(respFromServer.StatusCode)
	resp.Write([]byte(respFromServer.Status))
}

