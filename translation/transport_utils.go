// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"go.uber.org/zap"

	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/paramfilter"
	"github.com/xmidt-org/tr1d1um/transaction"

	"github.com/gorilla/mux"

	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/wrp-go/v5"
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
	if len(wdmp.Parameters) == 0 {
		return wdmp.Command == CommandTestSet // TEST_AND_SET can have empty parameters
	}

	var cmdSetAttr, cmdSet = 0, 0

	// validate parameters if it exists, even for TEST_SET
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
func wrap(WDMP []byte, tid string, pathVars map[string]string, partnerIDs []string, traceHeaders []string) (*wrp.Message, error) {
	canonicalDeviceID, err := wrp.ParseDeviceID(pathVars["deviceid"])
	if err != nil {
		return nil, transaction.NewBadRequestError(err)
	}
	return &wrp.Message{
		Type:            wrp.SimpleRequestResponseMessageType,
		Payload:         WDMP,
		Destination:     fmt.Sprintf("%s/%s", string(canonicalDeviceID), pathVars["service"]),
		TransactionUUID: tid,
		PartnerIDs:      partnerIDs,
		Headers:         traceHeaders,
	}, nil
}

type decodeRequestFunc func(context.Context, *http.Request) (*wrpRequest, error)

func decodeValidServiceRequest(services []string, decoder decodeRequestFunc) decodeRequestFunc {
	return func(c context.Context, r *http.Request) (*wrpRequest, error) {

		if !contains(mux.Vars(r)["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

func loadWDMP(encodedWDMP []byte, newCID, oldCID, syncCMC string) (*setWDMP, error) {
	wdmp := new(setWDMP)

	err := json.Unmarshal(encodedWDMP, wdmp)

	if err != nil && len(encodedWDMP) > 0 { // len(encodedWDMP) == 0 is ok as it is used for TEST_SET
		return nil, transaction.NewBadRequestError(fmt.Errorf("invalid WDMP structure: %s", err))
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

func captureWDMPParameters(filters *paramfilter.Filters) transaction.RequestFunc {
	return func(ctx context.Context, r *http.Request) context.Context {
		logger := sallust.Get(ctx)

		switch r.Method {
		case http.MethodGet:
			allNames := splitNames(r.FormValue("names"))
			if len(allNames) == 0 {
				return ctx
			}
			logger = logger.With(
				zap.String("command", CommandGet),
				zap.Strings("parameters", allNames),
			)
			ctx = sallust.With(ctx, logger)

			// The values of a GET are in the device's response, so the
			// filter is kept for the encoder to apply.  GET_ATTRIBUTES
			// answers with attributes, not values.
			if r.FormValue("attributes") == "" {
				ctx = context.WithValue(ctx, getValuesKey{}, &getValues{filter: filters.GetFilter()})
			}
			return ctx

		case http.MethodPatch:
			bodyBytes, _ := io.ReadAll(r.Body)
			r.Body.Close()

			r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
			wdmp, e := loadWDMP(bodyBytes, r.Header.Get(HeaderWPASyncNewCID), r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC))
			if e == nil {
				// Always log parameter names.
				logger = logger.With(
					zap.String("command", wdmp.Command),
					zap.Strings("parameters", getParamNames(wdmp.Parameters)),
				)

				// Conditionally log parameter values based on the filter.
				values := filterParamValues(wdmp.Parameters, filters.PatchFilter())
				if len(values) > 0 {
					logger = logger.With(zap.Any("parameterValues", values))
				}

				return sallust.With(ctx, logger)
			}
		}

		return ctx
	}
}

// filterParamValues returns a map of parameter name -> value for parameters
// whose values the filter allows to be logged.  If the filter is nil, no
// values are returned.
func filterParamValues(params []setParam, f *paramfilter.Filter) map[string]any {
	if f == nil {
		return nil
	}
	out := make(map[string]any)
	for _, p := range params {
		if p.Name != nil && f.ShouldLog(*p.Name) && p.Value != nil {
			out[*p.Name] = p.Value
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// extractParameters is the auth.ParamExtractor for the device routes: the
// TR-181 parameters a request names and what it does to them.  A GET reads
// the names it lists (values or attributes); a PATCH writes the parameters
// in its body; a POST, PUT or DELETE writes the table or row in its path,
// taken as a partial path.  A request this cannot read is left for the
// handler to reject.
func extractParameters(r *http.Request) (auth.ParamRequest, bool) {
	switch r.Method {
	case http.MethodGet:
		names := splitNames(r.FormValue("names"))
		if len(names) == 0 {
			return auth.ParamRequest{}, false
		}

		return auth.ParamRequest{Operation: auth.OperationRead, Names: names}, true

	case http.MethodPatch:
		bodyBytes, err := io.ReadAll(r.Body)
		r.Body.Close()
		r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
		if err != nil {
			return auth.ParamRequest{}, false
		}

		wdmp, err := loadWDMP(bodyBytes, r.Header.Get(HeaderWPASyncNewCID), r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC))
		if err != nil {
			return auth.ParamRequest{}, false
		}

		return auth.ParamRequest{Operation: auth.OperationWrite, Names: getParamNames(wdmp.Parameters)}, true

	case http.MethodPost, http.MethodPut, http.MethodDelete:
		path := strings.TrimSpace(mux.Vars(r)["parameter"])
		if path == "" {
			return auth.ParamRequest{}, false
		}

		return auth.ParamRequest{Operation: auth.OperationWrite, Names: []string{asPartialPath(path)}}, true
	}

	return auth.ParamRequest{}, false
}

// splitNames splits the names query of a GET into parameter names.  Each
// name is trimmed of surrounding whitespace and empty ones are dropped, so
// "a, b" and "a,,b" both name a and b.  The result is nil when nothing is
// left.  Every reader of the query uses this, so the names checked, logged
// and forwarded are the same.
func splitNames(names string) []string {
	var out []string
	for name := range strings.SplitSeq(names, ",") {
		if name = strings.TrimSpace(name); name != "" {
			out = append(out, name)
		}
	}

	return out
}

// asPartialPath gives a table or row name its trailing ".", so that it
// refers to everything beneath it.
func asPartialPath(name string) string {
	if strings.HasSuffix(name, ".") {
		return name
	}

	return name + "."
}

func getParamNames(params []setParam) (paramNames []string) {
	paramNames = make([]string, len(params))

	for i, param := range params {
		paramNames[i] = *param.Name
	}

	return
}

func contains(i string, elements []string) bool {
	return slices.Contains(elements, i)
}

// getValuesKey is the context key under which captureWDMPParameters leaves
// a getValues for a request whose response carries parameter values.
type getValuesKey struct{}

// getValues carries the GET filter to the encoder, and the values it lets
// through to the finalizer.  The encoder cannot hand the finalizer a new
// context, so they share this.
type getValues struct {
	filter *paramfilter.Filter
	values map[string]any
}

// encodeLoggingGetValues wraps next so that the values in a successful GET
// response are collected, as far as the GET filter allows, for
// finalizeLoggingGetValues to log.  The response the caller receives is
// next's, unchanged.
func encodeLoggingGetValues(next func(context.Context, http.ResponseWriter, *transaction.XmidtResponse) error) func(context.Context, http.ResponseWriter, *transaction.XmidtResponse) error {
	return func(ctx context.Context, w http.ResponseWriter, resp *transaction.XmidtResponse) error {
		if gv, ok := ctx.Value(getValuesKey{}).(*getValues); ok && gv.filter != nil && resp.Code == http.StatusOK {
			gv.values = getResponseValues(resp.Body, gv.filter)
		}

		return next(ctx, w, resp)
	}
}

// getResponseValues returns the name -> value pairs of a device's GET
// response that the filter allows to be logged.  A response that cannot be
// read yields none; encodeResponse reports that to the caller.
func getResponseValues(body []byte, f *paramfilter.Filter) map[string]any {
	msg := new(wrp.Message)
	if err := wrp.NewDecoderBytes(body, wrp.Msgpack).Decode(msg, wrp.NoStandardValidation()); err != nil {
		return nil
	}

	var payload struct {
		Parameters []setParam `json:"parameters"`
	}
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return nil
	}

	return filterParamValues(payload.Parameters, f)
}

// finalizeLoggingGetValues wraps next so that the response line carries the
// values encodeLoggingGetValues collected.
func finalizeLoggingGetValues(next transaction.FinalizerFunc) transaction.FinalizerFunc {
	return func(ctx context.Context, code int, r *http.Request) {
		if gv, ok := ctx.Value(getValuesKey{}).(*getValues); ok && len(gv.values) > 0 {
			ctx = sallust.With(ctx, sallust.Get(ctx).With(zap.Any("parameterValues", gv.values)))
		}

		next(ctx, code, r)
	}
}
