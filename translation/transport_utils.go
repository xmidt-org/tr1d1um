/**
 * Copyright 2021 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"

	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/transaction"
	"go.uber.org/zap"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/xmidt-org/webpa-common/v2/device"
	"github.com/xmidt-org/wrp-go/v3"
)

/* Functions that help decode a given SET request to TR1D1UM */

// deduceSET deduces the command for a given wdmp object
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

// isValidSetWDMP helps verify a given Set WDMP object is valid for its context
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

// getCommandForParams decides whether the command for some request is a 'SET' or 'SET_ATTRS' based on a given list of parameters
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

// wrp merges different values from a WDMP request into a WRP message
func wrap(WDMP []byte, tid string, pathVars map[string]string, partnerIDs []string) (*wrp.Message, error) {
	canonicalDeviceID, err := device.ParseID(pathVars["deviceid"])
	if err != nil {
		return nil, transaction.NewBadRequestError(err)
	}

	return &wrp.Message{
		Type:            wrp.SimpleRequestResponseMessageType,
		Payload:         WDMP,
		Destination:     fmt.Sprintf("%s/%s", string(canonicalDeviceID), pathVars["service"]),
		TransactionUUID: tid,
		PartnerIDs:      partnerIDs,
	}, nil
}

func decodeValidServiceRequest(services []string, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
	return func(c context.Context, r *http.Request) (interface{}, error) {

		if !contains(mux.Vars(r)["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

func loadWDMP(encodedWDMP []byte, newCID, oldCID, syncCMC string) (*setWDMP, error) {
	wdmp := new(setWDMP)

	err := json.Unmarshal(encodedWDMP, wdmp)

	if err != nil && len(encodedWDMP) > 0 { //len(encodedWDMP) == 0 is ok as it is used for TEST_SET
		return nil, transaction.NewBadRequestError(fmt.Errorf("Invalid WDMP structure. %s", err.Error()))
	}

	err = deduceSET(wdmp, newCID, oldCID, syncCMC)
	if err != nil {
		return nil, err
	}

	if !isValidSetWDMP(wdmp) {
		return nil, ErrInvalidSetWDMP
	}

	return wdmp, nil
}

func captureWDMPParameters(ctx context.Context, r *http.Request) (nctx context.Context) {
	nctx = ctx
	logger := sallust.Get(ctx)

	if r.Method == http.MethodPatch {
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()

		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))
		wdmp, e := loadWDMP(bodyBytes, r.Header.Get(HeaderWPASyncNewCID), r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC))
		if e == nil {

			logger = logger.With(
				zap.Reflect("command", wdmp.Command),
				zap.Reflect("parameters", getParamNames(wdmp.Parameters)),
			)

			nctx = sallust.With(ctx, logger)
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
	for _, e := range elements {
		if e == i {
			return true
		}
	}
	return false
}
