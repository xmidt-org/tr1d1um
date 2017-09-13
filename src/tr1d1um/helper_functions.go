package main

import (
	"github.com/Comcast/webpa-common/wrp"
	"encoding/json"
	"strings"
	"net/http"
)

//Given some wdmp data, wraps it into a wrp object, returns the resulting payload
func WrapInWrp(wdmpData []byte) (payload []byte, err error){
	wrpMessage := wrp.Message{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpData}
	payload, err = json.Marshal(wrpMessage)
	return
}

// All we care about is the payload. Method helps abstract away work done with the wdmp object
func GetFormattedData(req *http.Request, formValKey, sep string) (wdmpPayload[]byte, err error){
	wdmp := &WDMP{Command:req.Method, Names:strings.Split(req.FormValue(formValKey),sep)}

	wdmpPayload, err = json.Marshal(wdmp)
	return
}

