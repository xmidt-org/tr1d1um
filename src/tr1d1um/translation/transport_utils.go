package translation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"tr1d1um/common"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
)

/* Functions that help decode a given SET request to TR1D1UM */

//deduceSET deduces the command for a given wdmp object
func deduceSET(wdmp *setWDMP, newCID, oldCID, syncCMC string) (err error) {
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
func isValidSetWDMP(wdmp *setWDMP) (isValid bool) {
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
func wrap(WDMP []byte, tid string, pathVars map[string]string) (m *wrp.Message, err error) {
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

func decodeValidServiceRequest(services []string, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
	return func(c context.Context, r *http.Request) (interface{}, error) {

		if !contains(mux.Vars(r)["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

func logDecodedSETParameters(logger kitlog.Logger, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
	return func(c context.Context, r *http.Request) (request interface{}, err error) {
		if request, err = decoder(c, r); err == nil && r.Method == http.MethodPatch {
			var paramsLogger = kitlog.WithPrefix(logging.Info(logger),
				logging.MessageKey(), "Parameter Change Request")

			wrpRequest := (request).(*wrpRequest)
			wrpMsgPayload := wrpRequest.WRPMessage.Payload
			wdmp := new(setWDMP)

			if unmarshallErr := json.Unmarshal(wrpMsgPayload, wdmp); unmarshallErr == nil {
				tid := c.Value(common.ContextKeyRequestTID).(string)

				paramsLogger.Log("tid", tid, "command", wdmp.Command, "parameters", strings.Join(getParamNames(wdmp.Parameters), ","))

			}
		}

		return
	}
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
