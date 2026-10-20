// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/cast"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/tr1d1um/paramfilter"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v5"
)

const (
	contentTypeHeaderKey = "Content-Type"
	authHeaderKey        = "Authorization"

	// partnerIDHeader carries the partner IDs of a request whose credentials
	// state none.  wrp-go v3 defined it as wrphttp.PartnerIdHeader; v5 has no
	// wrphttp package.
	partnerIDHeader = "X-Xmidt-Partner-Id"
)

// Options wraps the properties needed to set up the translation server
type Options struct {
	S Service

	// APIRouter is assumed to be a subrouter with the API prefix path (i.e. 'api/v2')
	APIRouter *mux.Router

	Authenticate                *alice.Chain
	Log                         *zap.Logger
	ValidServices               []string
	ReducedLoggingResponseCodes []int
	BearerFingerprint           transaction.FingerprintConfig

	// ParamFilters controls which TR-181 parameter values are included in
	// log output for GET and PATCH operations.  A nil value logs none.
	ParamFilters *paramfilter.Filters

	// ParamCheck holds callers to the TR-181 parameters their parameter
	// capabilities name.  Nil when not configured.
	ParamCheck *auth.ParamCheck

	// Tracing supplies the propagator and header prefix used to pick up
	// trace context from incoming requests.
	Tracing candlelight.Tracing
}

// ConfigHandler sets up the server that powers the translation service
func ConfigHandler(c *Options) {
	WRPHandler := transaction.Handler[*wrpRequest, *transaction.XmidtResponse]{
		Before:      captureWDMPParameters(c.ParamFilters),
		Decode:      decodeValidServiceRequest(c.ValidServices, decodeRequest),
		Serve:       makeTranslationEndpoint(c.S),
		Encode:      encodeResponse,
		EncodeError: transaction.ErrorLogEncoder(sallust.Get, encodeError),
		Finalize:    transaction.Log(c.ReducedLoggingResponseCodes),
	}

	welcome := transaction.Welcome(c.BearerFingerprint)
	params := c.ParamCheck.Middleware(extractParameters)

	// the parameter check runs after authentication, so that the token it
	// reads has been verified
	c.APIRouter.Handle("/device/{deviceid}/{service}", c.Authenticate.Then(params(candlelight.EchoFirstTraceNodeInfo(c.Tracing, false)(welcome(WRPHandler))))).
		Methods(http.MethodGet, http.MethodPatch)

	c.APIRouter.Handle("/device/{deviceid}/{service}/{parameter}", c.Authenticate.Then(params(candlelight.EchoFirstTraceNodeInfo(c.Tracing, false)(welcome(WRPHandler))))).
		Methods(http.MethodDelete, http.MethodPut, http.MethodPost)
}

// getPartnerIDs returns the array that represents the partner-ids that were
// passed in as headers.  This function handles multiple duplicate headers.
func getPartnerIDs(h http.Header) []string {
	headers, ok := h[partnerIDHeader]
	if !ok {
		return nil
	}

	var partners []string

	for _, value := range headers {
		fields := strings.Split(value, ",")
		for i := range fields {
			fields[i] = strings.TrimSpace(fields[i])
		}
		partners = append(partners, fields...)
	}
	return partners
}

// getPartnerIDsDecodeRequest returns array of partnerIDs needed for decodeRequest
//
// The partner IDs of a JWT come from its claims.  Every other case falls back
// to the partner ID headers, which the caller controls.  That suits Basic
// auth, but would let the caller of a JWT without usable partner claims pick
// its own; auth's partner ID approver (jwtClaimPartnerIDsValidator) refuses
// such a JWT before the request gets here, and must stay in place for this
// fallback to be safe.
func getPartnerIDsDecodeRequest(ctx context.Context, r *http.Request) []string {
	token, ok := bascule.Get(ctx)
	// if there is no token
	if !ok {
		return getPartnerIDs(r.Header)
	}

	// if not jwt type
	switch token.(type) {
	case basculejwt.Claims:
	default:
		return getPartnerIDs(r.Header)
	}

	// if the jwt has no attributes
	accessor, ok := token.(bascule.AttributesAccessor)
	if !ok {
		return getPartnerIDs(r.Header)
	}

	partnerVal, ok := bascule.GetAttribute[any](accessor, auth.PartnerKeys...)
	// if no partner ids
	if !ok {
		return getPartnerIDs(r.Header)
	}

	// if the partner ids are not a list of strings
	partnerIDs, err := cast.ToStringSliceE(partnerVal)
	if err != nil {
		return getPartnerIDs(r.Header)
	}
	return partnerIDs
}

func getTID(ctx context.Context) string {
	t, ok := ctx.Value(transaction.ContextKeyRequestTID).(string)
	if !ok {
		sallust.Get(ctx).Warn(fmt.Sprintf("tid not found in header `%s` or generated", candlelight.HeaderWPATIDKeyName))
		return ""
	}

	return t
}

/* Request Decoding */
func decodeRequest(ctx context.Context, r *http.Request) (decodedRequest *wrpRequest, err error) {
	var (
		payload    []byte
		wrpMsg     *wrp.Message
		tid        string
		partnerIDs []string
	)

	if payload, err = requestPayload(r); err == nil {
		tid = getTID(ctx)
		partnerIDs = getPartnerIDsDecodeRequest(ctx, r)
	}

	if err == nil {
		var traceHeaders []string

		// If there's a traceparent, add it to traceHeaders array
		// Also, add tracestate to the traceHeaders array (can be empty)
		// A tracestate will not exist without a traceparent
		tp := r.Header.Get("traceparent")
		if tp != "" {
			tp = "traceparent: " + tp
			ts := r.Header.Get("tracestate")
			ts = "tracestate: " + ts
			traceHeaders = append(traceHeaders, tp, ts)
		}

		wrpMsg, err = wrap(payload, tid, mux.Vars(r), partnerIDs, traceHeaders)

		if err == nil {
			decodedRequest = &wrpRequest{
				WRPMessage:      wrpMsg,
				AuthHeaderValue: r.Header.Get(authHeaderKey),
			}
		}
	}

	return
}

func requestPayload(r *http.Request) (payload []byte, err error) {

	switch r.Method {
	case http.MethodGet:
		payload, err = requestGetPayload(r.FormValue("names"), r.FormValue("attributes"))
	case http.MethodPatch:
		payload, err = requestSetPayload(r.Body, r.Header.Get(HeaderWPASyncNewCID), r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC))
	case http.MethodDelete:
		payload, err = requestDeletePayload(mux.Vars(r))
	case http.MethodPut:
		payload, err = requestReplacePayload(mux.Vars(r), r.Body)
	case http.MethodPost:
		payload, err = requestAddPayload(mux.Vars(r), r.Body)
	default:
		// Unwanted methods should be filtered at the mux level. Thus, we "should" never get here
		err = ErrUnsupportedMethod
	}

	return
}

/* Response Encoding */
func encodeResponse(ctx context.Context, w http.ResponseWriter, resp *transaction.XmidtResponse) (err error) {
	// equivalent to forwarding all headers
	transaction.ForwardHeadersByPrefix("", resp.ForwardedHeaders, w.Header())

	// Write TransactionID for all requests
	tid := getTID(ctx)
	w.Header().Set(candlelight.HeaderWPATIDKeyName, tid)
	// just forward the XMiDT cluster response
	if len(resp.Body) == 0 && resp.Code == http.StatusOK {
		sallust.Get(ctx).Warn("sending 200 with an empty body")
		w.WriteHeader(resp.Code)
		return
	} else if resp.Code != http.StatusOK {
		w.WriteHeader(resp.Code)
		_, err = w.Write(resp.Body)
		return
	}

	wrpModel := new(wrp.Message)

	// The device's response is passed through, not acted on, so it is decoded
	// without wrp-go's standard validation: a response missing a field that
	// the standard requires must still reach the caller.
	if err = wrp.NewDecoderBytes(resp.Body, wrp.Msgpack).Decode(wrpModel, wrp.NoStandardValidation()); err == nil {

		// device response model
		var d struct {
			StatusCode int `json:"statusCode"`
		}

		w.Header().Set("Content-Type", "application/json")
		// use the device response status code if it's within 520-599 (inclusive) or 403
		// https://github.com/xmidt-org/tr1d1um/issues/354
		// https://github.com/xmidt-org/tr1d1um/issues/397
		if errUnmarshall := json.Unmarshal(wrpModel.Payload, &d); errUnmarshall == nil {
			if http.StatusForbidden == d.StatusCode || (520 <= d.StatusCode && d.StatusCode <= 599) {
				w.WriteHeader(d.StatusCode)
			}
		}

		_, err = w.Write(wrpModel.Payload)
	}

	return
}

/* Error Encoding */

func encodeError(ctx context.Context, err error, w http.ResponseWriter) {
	tid := getTID(ctx)
	w.Header().Set(contentTypeHeaderKey, "application/json")
	w.Header().Set(candlelight.HeaderWPATIDKeyName, tid)
	if ce, ok := errors.AsType[transaction.CodedError](err); ok {
		w.WriteHeader(ce.StatusCode())
	} else {
		w.WriteHeader(http.StatusInternalServerError)

		// the real error is logged into our system before encodeError() is called
		// the idea behind masking it is to not send the external API consumer internal error messages
		err = transaction.ErrTr1d1umInternal
	}

	_ = json.NewEncoder(w).Encode(map[string]any{
		"message": err.Error(),
	})

}

/* Request-type specific decoding functions */

func requestSetPayload(in io.Reader, newCID, oldCID, syncCMC string) (p []byte, err error) {
	var (
		wdmp *setWDMP
		data []byte
	)

	if data, err = io.ReadAll(in); err == nil {
		if wdmp, err = loadWDMP(data, newCID, oldCID, syncCMC); err == nil {
			return json.Marshal(wdmp)
		}
	}

	return
}

func requestGetPayload(names, attributes string) ([]byte, error) {
	split := splitNames(names)
	if len(split) == 0 {
		return nil, ErrEmptyNames
	}

	wdmp := new(getWDMP)

	// default values at this point
	wdmp.Names, wdmp.Command = split, CommandGet

	if attributes != "" {
		wdmp.Command, wdmp.Attributes = CommandGetAttrs, attributes
	}

	return json.Marshal(wdmp)
}

func requestAddPayload(m map[string]string, input io.Reader) (p []byte, err error) {
	var wdmp = &addRowWDMP{Command: CommandAddRow}

	table := m["parameter"]

	if len(table) < 1 {
		return nil, ErrMissingTable
	}

	wdmp.Table = table

	payload, err := io.ReadAll(input)

	if err != nil {
		return nil, ErrInvalidPayload
	}

	if len(payload) < 1 {
		return nil, ErrMissingRow
	}

	err = json.Unmarshal(payload, &wdmp.Row)
	if err != nil {
		return nil, ErrInvalidRow
	}
	return json.Marshal(wdmp)
}

func requestReplacePayload(m map[string]string, input io.Reader) ([]byte, error) {
	var wdmp = &replaceRowsWDMP{Command: CommandReplaceRows}

	table := strings.Trim(m["parameter"], " ")
	if len(table) == 0 {
		return nil, ErrMissingTable
	}

	wdmp.Table = table

	payload, err := io.ReadAll(input)

	if err != nil {
		return nil, err
	}

	if len(payload) < 1 {
		return nil, ErrMissingRows
	}

	err = json.Unmarshal(payload, &wdmp.Rows)
	if err != nil {
		return nil, ErrInvalidRows
	}

	return json.Marshal(wdmp)
}

func requestDeletePayload(m map[string]string) ([]byte, error) {
	row := m["parameter"]
	if len(row) < 1 {
		return nil, ErrMissingRow
	}
	return json.Marshal(&deleteRowDMP{Command: CommandDeleteRow, Row: row})
}
