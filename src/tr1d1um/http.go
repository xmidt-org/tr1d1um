package main

import (
	"net/http"
	"strings"
	"encoding/json"
	"time"
	"bytes"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-kit/kit/log"
)
type ConversionHandler struct {
	log log.Logger
	timeOut time.Duration
	targetUrl string
}

func (sh ConversionHandler) ConversionGETHandler(resp http.ResponseWriter, req *http.Request){
	wdmp := new(WDMP)

	//read in parameters and command
	wdmp.Names = strings.Split(req.FormValue("names"), ",")
	wdmp.Command = req.Method

	//Get payload for transfer
	wdmpPayload, err := json.Marshal(wdmp)

	if err != nil {
		return
	}

	wrpMessage := wrp.Message{}
	wrpMessage.Payload = wdmpPayload
	wrpPayload, err := json.Marshal(wrpMessage)

	if err != nil {
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
		return
	}

	respFromServer, err := clientWithDeadline.Do(requestToServer)

	if err != nil{
		resp.WriteHeader(http.StatusInternalServerError)
		resp.Write([]byte("Error while posting request"))
		return
	}

	//Try forwarding back the response to the initial requester
	resp.WriteHeader(respFromServer.StatusCode)
	resp.Write([]byte(respFromServer.Status))
}

/*
func AttemptEncoding(message *wrp.Message, encode func(interface{}, wrp.Format) ([]byte)) (encoded []byte, err error) {
   if encode == nil {
	   err = errors.New("encode method is nil")
	   return
   }
   defer func() {
	   panicked := recover()
	   if panicked != nil {
		   err = panicked.(error) //recover from encoding panic
	   }
   }()

   encoded = encode(message, wrp.Msgpack)
}
*/

/*
func getConfigHandler(resp http.ResponseWriter, req *http.Request) {
	tid, deviceId, service, ok := ConfigRequirements(resp, req, "GET")
	if !ok {
		return
	}

	var paramArray []string
	if service == "stat" {
		//log.Debug("tid %v - service is \"%s\".  sending empty paramArray len %v: [%v]", tid, service, len(paramArray), paramArray)
	} else {
		syncName := req.FormValue("filter")
		syncList, exists := tConfig.GetSyncList.GetList(syncName)
		if exists {
			paramArray = syncList
			//log.Debug("tid %v - from getSync paramArray len %v: [%v]", tid, len(paramArray), paramArray)
		} else {
			params := req.FormValue("names")
			//log.Debug("tid %v - params : [%v]", tid, params)
			if params == "" {
				//log.Error("tid %v - empty names in GET request", tid)
				ts.ResponseJsonErr(resp, "names parameter is required to be valid", http.StatusBadRequest)
				return
			}
			paramArray = strings.Split(params, ",")
			//log.Debug("tid %v - from names paramArray len %v: [%v]", tid, len(paramArray), paramArray)
		}
	}

	xpcMsg := new(ts.XPCGetMessage)
	for _, p := range paramArray {
		//valid param
		pNoQ := strings.Replace(p, "\"", "", -1)
		if paramRegex != nil && !paramRegex.Match([]byte(pNoQ)) {
			//log.Error("tid %v - invalid Parameter %v", tid, p)
			ts.ResponseJsonErr(resp, "Invalid parameter", http.StatusBadRequest)
			return
		}
		xpcMsg.Names = append(xpcMsg.Names, pNoQ)
	}
	xpcMsg.Command = ts.COMMAND_GET

	attrs := req.FormValue("attributes")
	if attrs == "" {
		//log.Trace("tid %v - no attributes in GET request", tid)
	} else {
		noQ := strings.Replace(attrs, "\"", "", -1)
		xpcMsg.Attributes = noQ

		xpcMsg.Command = ts.COMMAND_GET_ATTRS
	}

	//log.Trace("tid %v -xpcMsg : [%#v]", tid, xpcMsg)
	if !xpcMsg.IsValid() && service != "stat" {
		//log.Error("tid %v - invalid XPC GET message", tid)
		ts.ResponseJsonErr(resp, "invalid XPC GET message", http.StatusBadRequest)
		return
	}

	reqBytes, err := json.Marshal(xpcMsg)
	if err != nil {
		//log.Error("tid %v - JSON decoding XPC Msg error %v", tid, err)
		ts.ResponseJsonErr(resp, "JSON decoding XPC GET Msg error", http.StatusBadRequest)
		return
	}


	//todo: with tid and device id be needed?
	//todo: for now, print them :)
	fmt.Printf("tid=%s, deviceid=%s\n", tid, deviceId)

	PostWithDeadline(resp, reqBytes)

	return
}

func ConfigRequirements(resp http.ResponseWriter, req *http.Request, method string) (string, string, string, bool) {
	// get tid
	tid := GetTidOrDefault(req)

	// get path parameter
	hdr_device_id, _, ok := ts.GetMACFromReq(req)
	if !ok {
		//log.Error("tid %v - incorrect req device id %v , mac [%v]", tid, hdr_device_id, mac)
		ts.ResponseJsonErr(resp, "incorrect device id " + hdr_device_id, http.StatusBadRequest)
		return tid, "", "", false
	}

	// get device id
	deviceId, err := ts.CanonicalDeviceID(hdr_device_id)
	if err != nil {
		//log.Error("tid %v - invalid canonical device id , mac [%v]", tid, deviceId, mac)
		ts.ResponseJsonErr(resp, "invalid device id "+deviceId, http.StatusBadRequest)
		return tid, "", "", false
	}
	//log.Debug("tid %v - device id [%v], mac [%v]", tid, deviceId, mac)

	reqVal := ""
	vars := mux.Vars(req)
	if vars["service"] != "" {
		// check service
		if _, ok = checkService(resp, req, tid); !ok {
			return tid, deviceId, reqVal, false
		} else if vars["service"] == "stat" && method != "GET" {
			return tid, deviceId, reqVal, false
		}

		reqVal = vars["service"]

	} else if vars["eventtype"] != "" {
		_ , ok := ts.GetEventTypeFromReqUrl(req)
		//log.Debug("tid %v - Req eventType: [%v] ", tid, eventType)
		if !ok {
			//log.Error("tid %v - incorrect eventType %v", tid, eventType)
			ts.ResponseJsonErr(resp, "incorrect eventType", http.StatusBadRequest)
			return tid, deviceId, reqVal, false
		}

		reqVal = vars["eventtype"]

	} else {
		//log.Debug("tid %v - no request var found", tid)
		ts.ResponseJsonErr(resp, "no request var found", http.StatusBadRequest)
		return tid, deviceId, "", false
	}

	// configuration requirements ok
	return tid, deviceId, reqVal, true
}



func checkService(resp http.ResponseWriter, req *http.Request, tid string) (string, bool) {
	service, ok := ts.GetValuFromReqVar(req, "service")
	if !ok {
		//log.Error("tid %v - missing reqeust service %v ", tid, service)
		ts.ResponseJsonErr(resp, "missing reqeust service ", http.StatusBadRequest)
		return "", false
	}
	//check white list
	//log.Debug("tid %v - destsvc [%v]", tid, service)
	if !isValidService(service) {
		//log.Error("tid %v - incorrect request service %v ", tid, service)
		ts.ResponseJsonErr(resp, "incorrect request service", http.StatusBadRequest)
		return "", false
	}
	return service, true
}

func isValidService(service string) (ok bool) {
	for _, svc := range tConfig.ServiceList {
		if service == svc {
			return true
		}
	}
	return false
}

func GetTidOrDefault(req *http.Request)(tid string){
	tid, ok := ts.GetTidFromReq(req)
	if !ok {
		tid = ts.NewUUID()
	}
	return
}

*/
