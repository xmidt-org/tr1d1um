package translation

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/tr1d1um/common"
	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
)

//genTID generates a 16-byte long string
func genTID() (tid string, err error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}

/* Functions that help decode a given SET request to TR1D1UM */

//validateAndDeduceSET deduces the command for a given wdmp object and validates it for such
func validateAndDeduceSET(wdmp *setWDMP, newCID, oldCID, syncCMC string) (err error) {
	if newCID == "" && oldCID != "" {
		return ErrNewCIDRequired
	} else if newCID == "" && oldCID == "" && syncCMC == "" {
		wdmp.Command = getCommandForParams(wdmp.Parameters)
	} else {
		wdmp.Command = CommandTestSet
		wdmp.NewCid, wdmp.OldCid, wdmp.SyncCmc = newCID, oldCID, syncCMC
	}

	if !isValidSetWDMP(wdmp) {
		return ErrInvalidSetWDMP
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
	if params == nil || len(params) < 1 {
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

func wrap(WDMP []byte, tid string, pathVars map[string]string) (m *wrp.Message, err error) {
	var canonicalDeviceID device.ID

	if canonicalDeviceID, err = device.ParseID(pathVars["deviceid"]); err == nil {
		service := pathVars["service"]

		if tid == "" {
			if tid, err = genTID(); err != nil {
				return
			}
		}

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

		if vars := mux.Vars(r); vars == nil || !contains(vars["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

func forwardHeadersByPrefix(prefix string, resp *http.Response, w http.ResponseWriter) {
	if resp != nil {
		for headerKey, headerValues := range resp.Header {
			if strings.HasPrefix(headerKey, prefix) {
				for _, headerValue := range headerValues {
					w.Header().Add(headerKey, headerValue)
				}
			}
		}
	}
}

func contains(i string, elements []string) bool {
	for _, e := range elements {
		if e == i {
			return true
		}
	}
	return false
}

func transactionLogging(logger kitlog.Logger) kithttp.ServerFinalizerFunc {
	return func(ctx context.Context, code int, r *http.Request) {

		transactionLogger := kitlog.WithPrefix(logging.Info(logger),
			logging.MessageKey(), "Bookkeeping response",
			"requestAddress", r.RemoteAddr,
			"requestURLPath", r.URL.Path,
			"requestURLQuery", r.URL.RawQuery,
			"requestMethod", r.Method,
			"responseCode", code,
			"responseHeaders", ctx.Value(kithttp.ContextKeyResponseHeaders),
			"responseError", ctx.Value(common.ContextKeyResponseError),
		)

		var latency = "-"

		if requestArrivalTime, ok := ctx.Value(common.ContextKeyRequestArrivalTime).(time.Time); ok {
			latency = fmt.Sprintf("%v", time.Now().Sub(requestArrivalTime))
		} else {
			logging.Error(logger).Log(logging.ErrorKey(), "latency value could not be derived")
		}

		transactionLogger.Log("latency", latency)
	}
}
