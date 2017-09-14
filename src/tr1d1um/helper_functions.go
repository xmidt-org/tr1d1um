package main

import (
	"github.com/Comcast/webpa-common/wrp"
	"encoding/json"
	"strings"
	"net/http"
	"io"
	"errors"
)

var (
	ErrJsonEmpty   = errors.New("JSON payload is empty")
)

//Given some wdmp data, wraps it into a wrp object, returns the resulting payload
func WrapInWrp(wdmpData []byte) (payload []byte, err error){
	wrpMessage := wrp.Message{Type:wrp.SimpleRequestResponseMessageType, Payload:wdmpData}
	payload, err = json.Marshal(wrpMessage)
	return
}

// All we care about is the payload. Method helps abstract away work done with the WDMP object
func GetFlavorFormat(req *http.Request, attr, formValKey, sep string) (payload[]byte, err error){
	wdmp := new(GetWDMP)

	if names := strings.Split(req.FormValue(formValKey),sep); len(names) > 0 {
		wdmp.Command = COMMAND_GET
		wdmp.Names = names
	} else{
		err = errors.New("names is a required property for GET")
		return
	}

	if attributes := req.FormValue(attr); attributes != "" {
		wdmp.Command = COMMAND_GET_ATTRS
	}

	payload, err = json.Marshal(wdmp)
	return
}

func SetFlavorFormat(req *http.Request, ReadEntireBody func(io.Reader)(payload []byte, err error)) (payload[]byte, err error){
	wdmp := new(SetWDMP)
	DecodeJsonPayload(req, wdmp, ReadEntireBody)

	wdmp.Command, err = ValidateAndGetCommand(req, wdmp)

	if err != nil {
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

func ValidateAndGetCommand(req *http.Request, wdmp *SetWDMP) (command string, err error){
	if newCid := req.Header.Get(HEADER_WPA_SYNC_NEW_CID); newCid != "" {
		wdmp.OldCid = req.Header.Get(HEADER_WPA_SYNC_OLD_CID)
		wdmp.NewCid = newCid
		wdmp.SyncCmc =  req.Header.Get(HEADER_WPA_SYNC_CMC)
		command, err = validateSETParams(false, wdmp, COMMAND_TEST_SET)
	} else {
		command, err = validateSETParams(true, wdmp, "")
	}
	return
}


/*  -Inputs-:
 **checkingForSetAttr**: true if we're checking for the required parameter properties for the SET_ATTRIBUTES command
		These properties are: attributes and name

 **wdmp**: the WDMP object from which we retrieve the parameters

 **override**: overrides the final suggested command if non-empty. Useful if one just wants to check for SET command
		parameter properties (value, dataType, name)

 	-Outputs-:
 **command**: the final command based on the analysis of the parameters
 **err**: it is non-nil if any required property is violated
*/
func validateSETParams(checkingForSetAttr bool, wdmp *SetWDMP, override string) (command string, err error){
	for _, sp := range wdmp.Parameters {
		if sp.Name == nil {
			err = errors.New("name is required for parameters")
			return
		}

		if checkingForSetAttr {
			if sp.Value != nil || sp.Attributes == nil {
				checkingForSetAttr = false
			}
		} else { //in this case, we are just checking valid parameters for SET
			if sp.DataType == nil || sp.Value == nil {
				err = errors.New("dataType and value are required for SET command")
			}
		}
	}

	if override != "" {
		command = override
		return
	}

	if checkingForSetAttr { // checked for SET_ATTRS properties until the end and found no violation
		command = COMMAND_SET_ATTRS
		return
	}

	command = COMMAND_SET
	return
}

func DecodeJsonPayload(req *http.Request, v interface{}, ReadEntireBody func(io.Reader)([]byte, error)) (err error) {
	if ReadEntireBody == nil {
		err = errors.New("method ReadEntireBody is undefined")
		return
	}
	payload, err := ReadEntireBody(req.Body)
	req.Body.Close()

	if err != nil {
		return
	}

	if len(payload) == 0 {
		err = ErrJsonEmpty
		return
	}

	err = json.Unmarshal(payload, v)
	if err != nil {
		return
	}
	return
}

