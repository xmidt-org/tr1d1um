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

	"github.com/spf13/cast"
	"github.com/xmidt-org/bascule"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"go.uber.org/zap"

	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v3"
	"github.com/xmidt-org/wrp-go/v3/wrphttp"
)

const (
	contentTypeHeaderKey = "Content-Type"
	authHeaderKey        = "Authorization"
)

// Options wraps the properties needed to set up the translation server
type Options struct {
	S Service

	//APIRouter is assumed to be a subrouter with the API prefix path (i.e. 'api/v2')
	APIRouter *mux.Router

	Authenticate                *alice.Chain
	Log                         *zap.Logger
	ValidServices               []string
	ReducedLoggingResponseCodes []int
	BearerFingerprint           transaction.FingerprintConfig

	// PartnerIDs governs where partner IDs may come from.
	PartnerIDs PartnerIDOptions
}

// PartnerIDSource names where a request's partner IDs came from.  These are
// metric label values: watching the header source drain to zero is how a
// deployment knows it can leave AllowHeader false.
const (
	PartnerIDSourceToken   = "token"
	PartnerIDSourceHeader  = "header"
	PartnerIDSourceEmpty   = "empty"
	PartnerIDSourceRefused = "refused"
)

// PartnerIDOptions governs the sources permitted for the partner IDs stamped on
// outbound WRP messages.
type PartnerIDOptions struct {
	// AllowHeader lets the X-Webpa-Partner-Id header supply partner IDs for
	// callers whose token states none.
	AllowHeader bool

	// AllowEmpty lets a request proceed with no partner IDs at all.
	AllowEmpty bool

	// Record counts which source supplied a request's partner IDs.  Optional.
	Record func(source string)
}

// record reports the source used, when a recorder is configured.
func (o PartnerIDOptions) record(source string) {
	if o.Record != nil {
		o.Record(source)
	}
}

// ConfigHandler sets up the server that powers the translation service
func ConfigHandler(c *Options) {
	opts := []kithttp.ServerOption{
		kithttp.ServerBefore(captureWDMPParameters),
		kithttp.ServerErrorEncoder(transaction.ErrorLogEncoder(sallust.Get, encodeError)),
		kithttp.ServerFinalizer(transaction.Log(c.ReducedLoggingResponseCodes)),
	}

	WRPHandler := kithttp.NewServer(
		makeTranslationEndpoint(c.S),
		decodeValidServiceRequest(c.ValidServices, makeDecodeRequest(c.PartnerIDs)),
		encodeResponse,
		opts...,
	)

	welcome := transaction.Welcome(c.BearerFingerprint)

	c.APIRouter.Handle("/device/{deviceid}/{service}", c.Authenticate.Then(candlelight.EchoFirstTraceNodeInfo(candlelight.Tracing{}, false)(welcome(WRPHandler)))).
		Methods(http.MethodGet, http.MethodPatch)

	c.APIRouter.Handle("/device/{deviceid}/{service}/{parameter}", c.Authenticate.Then(candlelight.EchoFirstTraceNodeInfo(candlelight.Tracing{}, false)(welcome(WRPHandler)))).
		Methods(http.MethodDelete, http.MethodPut, http.MethodPost)
}

// getPartnerIDs returns the array that represents the partner-ids that were
// passed in as headers.  This function handles multiple duplicate headers.
func getPartnerIDs(h http.Header) []string {
	headers, ok := h[wrphttp.PartnerIdHeader]
	if !ok {
		return nil
	}

	var partners []string

	for _, value := range headers {
		fields := strings.Split(value, ",")
		for i := 0; i < len(fields); i++ {
			fields[i] = strings.TrimSpace(fields[i])
		}
		partners = append(partners, fields...)
	}
	return partners
}

// ErrNoPartnerIDs is returned when neither the token nor an allowed header
// supplies a partner ID.
var ErrNoPartnerIDs = transaction.NewBadRequestError(
	errors.New("no partner IDs presented"))

// JWT claims read from a token.
const (
	allowedResourcesClaim = "allowedResources"
	allowedPartnersClaim  = "allowedPartners"
)

// partnerIDClaimPath is where a JWT states the partners its bearer may act for.

var partnerIDClaimPath = []string{allowedResourcesClaim, allowedPartnersClaim}

// getPartnerIDsDecodeRequest returns the partner IDs to stamp on the outbound
// WRP message.
//
// tr1d1um cannot tell which partner owns the target device, so it does not
// authorize the choice; it states the partner and the device confirms or
// rejects it.  What it can insist on is that the statement comes from the
// verified token whenever the token makes one.
//
// The X-Webpa-Partner-Id header is consulted only for callers whose token
// carries no partner IDs, and only when a deployment has allowed it.  With no
// source at all the request is refused, unless the deployment has said an
// unscoped request is acceptable.
func getPartnerIDsDecodeRequest(ctx context.Context, r *http.Request, cfg PartnerIDOptions) ([]string, error) {
	if partners := partnerIDsFromToken(ctx); len(partners) > 0 {
		cfg.record(PartnerIDSourceToken)
		return partners, nil
	}

	if cfg.AllowHeader {
		if partners := getPartnerIDs(r.Header); len(partners) > 0 {
			cfg.record(PartnerIDSourceHeader)
			return partners, nil
		}
	}

	if cfg.AllowEmpty {
		cfg.record(PartnerIDSourceEmpty)
		return nil, nil
	}

	cfg.record(PartnerIDSourceRefused)
	return nil, ErrNoPartnerIDs
}

// partnerIDsFromToken reads the partner IDs the verified token states.
func partnerIDsFromToken(ctx context.Context) []string {
	token, ok := bascule.Get(ctx)
	if !ok {
		return nil
	}

	accessor, ok := token.(bascule.AttributesAccessor)
	if !ok {
		return nil
	}

	value, found := bascule.GetAttribute[any](accessor, partnerIDClaimPath...)
	if !found {
		return nil
	}

	partners, err := cast.ToStringSliceE(value)
	if err != nil {
		// The claim is present but not a list of strings.  That is a problem
		// with the token rather than with this service, so the request carries
		// on to whatever fallback is configured -- but silently discarding it
		// would leave a refused request with no explanation.
		sallust.Get(ctx).Warn("partner IDs claim is not a list of strings",
			zap.String("claim", strings.Join(partnerIDClaimPath, ".")),
			zap.Error(err))
		return nil
	}

	return partners
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

// makeDecodeRequest builds the request decoder.  It is a closure so the partner
// authorizer can be supplied from configuration rather than reached for
// globally.
func makeDecodeRequest(cfg PartnerIDOptions) kithttp.DecodeRequestFunc {
	return func(ctx context.Context, r *http.Request) (interface{}, error) {
		return decodeRequest(ctx, r, cfg)
	}
}

func decodeRequest(ctx context.Context, r *http.Request, cfg PartnerIDOptions) (decodedRequest interface{}, err error) {
	var (
		payload    []byte
		wrpMsg     *wrp.Message
		tid        string
		partnerIDs []string
	)

	if payload, err = requestPayload(r); err == nil {
		tid = getTID(ctx)
		partnerIDs, err = getPartnerIDsDecodeRequest(ctx, r, cfg)
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
		//Unwanted methods should be filtered at the mux level. Thus, we "should" never get here
		err = ErrUnsupportedMethod
	}

	return
}

/* Response Encoding */
func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	var resp = response.(*transaction.XmidtResponse)

	//equivalent to forwarding all headers
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

	if err = wrp.NewDecoderBytes(resp.Body, wrp.Msgpack).Decode(wrpModel); err == nil {

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
	var ce transaction.CodedError
	if errors.As(err, &ce) {
		w.WriteHeader(ce.StatusCode())
	} else {
		w.WriteHeader(http.StatusInternalServerError)

		//the real error is logged into our system before encodeError() is called
		//the idea behind masking it is to not send the external API consumer internal error messages
		err = transaction.ErrTr1d1umInternal
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		// nolint: goconst
		"message": err.Error(),
	})

}

/* Request-type specific decoding functions */

func requestSetPayload(in io.Reader, newCID, oldCID, syncCMC string) (p []byte, err error) {
	var (
		wdmp = new(setWDMP)
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
	if len(names) < 1 {
		return nil, ErrEmptyNames
	}

	wdmp := new(getWDMP)

	//default values at this point
	wdmp.Names, wdmp.Command = strings.Split(names, ","), CommandGet

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
