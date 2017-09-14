package main
import (
	"net/http"
	"time"
	"bytes"
	"github.com/go-kit/kit/log"
	"github.com/Comcast/webpa-common/logging"
	"io/ioutil"
	"io"
)
type ConversionHandler struct {
	infoLogger log.Logger
	errorLogger log.Logger
	timeOut time.Duration
	targetUrl string
	GetFlavorFormat func(*http.Request, string, string, string) ([]byte, error)
	SetFlavorFormat func(*http.Request, func(io.Reader)([]byte,error)) ([]byte, error)
	WrapInWrp func([]byte) ([]byte, error)
}

func (sh ConversionHandler) ConversionGETHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.GetFlavorFormat(req, "attributes", "names", ",")

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpPayload, err := sh.WrapInWrp(wdmpPayload)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(),ERR_UNSUCCESSFUL_DATA_WRAP, logging.ErrorKey(), err.Error())
		return
	}

	sh.SendData(resp, wrpPayload)
}

func (sh ConversionHandler) ConversionSETHandler(resp http.ResponseWriter, req *http.Request){
	wdmpPayload, err := sh.SetFlavorFormat(req, ioutil.ReadAll)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_PARSE, logging.ErrorKey(), err.Error())
		return
	}

	wrpPayload, err := sh.WrapInWrp(wdmpPayload)

	if err != nil {
		sh.errorLogger.Log(logging.MessageKey(), ERR_UNSUCCESSFUL_DATA_WRAP, logging.ErrorKey(), err.Error())
		return
	}

	sh.SendData(resp, wrpPayload)
}

func (sh ConversionHandler) SendData(resp http.ResponseWriter, payload []byte){
	clientWithDeadline := http.Client{Timeout:sh.timeOut}

	//todo: any headers to be added here
	requestToServer, err := http.NewRequest("GET", sh.targetUrl, bytes.NewBuffer(payload))
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

