package common

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/wrp"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
)

/* Functions that help decode a given SET request to TR1D1UM */

//deduceSET deduces the command for a given wdmp object
func deduceSET(wdmp *SetWDMP, newCID, oldCID, syncCMC string) (err error) {
	if newCID == "" && oldCID != "" {
		return ErrNewCIDRequired
	} else if newCID == "" && oldCID == "" && syncCMC == "" {
		wdmp.Command = getCommandForParams(wdmp.Parameters)
	} else {
		wdmp.Command = CommandTestSet
		wdmp.NewCid, wdmp.OldCid, wdmp.SyncCmc = newCID, oldCID, syncCMC
	}

	return
}

//isValidSetWDMP helps verify a given Set WDMP object is valid for its context
func isValidSetWDMP(wdmp *SetWDMP) (isValid bool) {
	if emptyParams := wdmp.Parameters == nil || len(wdmp.Parameters) == 0; emptyParams {
		return wdmp.Command == CommandTestSet //TEST_AND_SET can have empty parameters
	}

	var cmdSetAttr, cmdSet = 0, 0

	//validate parameters if it exists, even for TEST_SET
	for _, param := range wdmp.Parameters {
		if param.Name == nil || *param.Name == "" {
			return
		}

		if param.Value != nil && (param.DataType == nil || *param.DataType < 0) {
			return
		}

		if wdmp.Command == CommandSetAttrs && param.Attributes == nil {
			return
		}

		if param.Attributes != nil &&
			param.DataType == nil &&
			param.Value == nil {

			cmdSetAttr++
		} else {
			cmdSet++
		}

		// verify that all parameters are correct for either doing a command SET_ATTRIBUTE or SET
		if cmdSetAttr > 0 && cmdSet > 0 {
			return
		}
	}
	return true
}

//getCommandForParams decides whether the command for some request is a 'SET' or 'SET_ATTRS' based on a given list of parameters
func getCommandForParams(params []setParam) (command string) {
	command = CommandSet
	if len(params) < 1 {
		return
	}
	if wdmp := params[0]; wdmp.Attributes != nil &&
		wdmp.Name != nil &&
		wdmp.DataType == nil &&
		wdmp.Value == nil {
		command = CommandSetAttrs
	}
	return
}

/* Other transport-level helper functions */

//wrp merges different values from a WDMP request into a WRP message
func Wrap(WDMP []byte, tid string, pathVars map[string]string) (m *wrp.Message, err error) {
	var canonicalDeviceID device.ID

	if canonicalDeviceID, err = device.ParseID(pathVars["deviceid"]); err == nil {
		service := pathVars["service"]

		m = &wrp.Message{
			Type:            wrp.SimpleRequestResponseMessageType,
			Payload:         WDMP,
			Destination:     fmt.Sprintf("%s/%s", string(canonicalDeviceID), service),
			TransactionUUID: tid,
			Source:          service,
		}
	}
	return
}

//DecodeValidServiceRequest filters out requests made to an unsupported service before it reaches the service decoder
func DecodeValidServiceRequest(services []string, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
	return func(c context.Context, r *http.Request) (interface{}, error) {

		if !contains(mux.Vars(r)["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

//LoadWDMP loads a given WDMP payload into an expected WDMP data structure
func LoadWDMP(encodedWDMP []byte, newCID, oldCID, syncCMC string) (wdmp *SetWDMP, err error) {
	wdmp = new(SetWDMP)

	if err = json.Unmarshal(encodedWDMP, wdmp); err == nil || len(encodedWDMP) == 0 { //len(data) == 0 case is for TEST_SET
		if err = deduceSET(wdmp, newCID, oldCID, syncCMC); err == nil {
			if !isValidSetWDMP(wdmp) {
				err = ErrInvalidSetWDMP
			}
		}
	}

	return
}

func getParamNames(params []setParam) (paramNames []string) {
	paramNames = make([]string, len(params))

	for i, param := range params {
		paramNames[i] = *param.Name
	}

	return
}

func contains(i string, elements []string) bool {
	if elements != nil {
		for _, e := range elements {
			if e == i {
				return true
			}
		}
	}
	return false
}
