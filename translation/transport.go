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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/cast"
	"go.uber.org/zap"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculechecks"
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
}

// ConfigHandler sets up the server that powers the translation service
func ConfigHandler(c *Options) {
	opts := []kithttp.ServerOption{
		kithttp.ServerBefore(captureWDMPParameters),
		kithttp.ServerErrorEncoder(transaction.ErrorLogEncoder(sallust.Get, encodeError)),
		kithttp.ServerFinalizer(transaction.Log(c.Log, c.ReducedLoggingResponseCodes)),
	}

	WRPHandler := kithttp.NewServer(
		makeTranslationEndpoint(c.S),
		decodeValidServiceRequest(c.ValidServices, decodeRequest),
		encodeResponse,
		opts...,
	)

	c.APIRouter.Handle("/device/{deviceid}/{service}", c.Authenticate.Then(transaction.Welcome(WRPHandler))).
		Methods(http.MethodGet, http.MethodPatch)

	c.APIRouter.Handle("/device/{deviceid}/{service}/{parameter}", c.Authenticate.Then(transaction.Welcome(WRPHandler))).
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

// getPartnerIDsDecodeRequest returns array of partnerIDs needed for decodeRequest
func getPartnerIDsDecodeRequest(ctx context.Context, r *http.Request) []string {
	auth, ok := bascule.FromContext(ctx)
	//if no token
	if !ok {
		return getPartnerIDs(r.Header)
	}
	tokenType := auth.Token.Type()
	//if not jwt type
	if tokenType != "jwt" {
		return getPartnerIDs(r.Header)
	}
	partnerVal, ok := bascule.GetNestedAttribute(auth.Token.Attributes(), basculechecks.PartnerKeys()...)
	//if no partner ids
	if !ok {
		return getPartnerIDs(r.Header)
	}
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
func decodeRequest(ctx context.Context, r *http.Request) (decodedRequest interface{}, err error) {
	var (
		payload []byte
		wrpMsg  *wrp.Message
	)
	if payload, err = requestPayload(r); err == nil {
		tid := getTID(ctx)
		partnerIDs := getPartnerIDsDecodeRequest(ctx, r)
		var traceHeaders []string

		// If there's a traceparent, add it to traceHeaders string
		tp := r.Header.Get("traceparent")
		if tp != "" {
			tp = "traceparent: " + tp
			traceHeaders = append(traceHeaders, tp)
		}

		// If there's a tracestatus, add it to traceHeaders string
		ts := r.Header.Get("tracestatus")
		if ts != "" {
			ts = "tracestatus: " + ts
			traceHeaders = append(traceHeaders, ts)
		}

		if len(traceHeaders) > 0 {
			wrpMsg, err = wrap(payload, tid, mux.Vars(r), partnerIDs, traceHeaders)
		} else {
			wrpMsg, err = wrap(payload, tid, mux.Vars(r), partnerIDs, nil)
		}

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
		// use the device response status code if it's within 520-599 (inclusive)
		// https://github.com/xmidt-org/tr1d1um/issues/354
		if errUnmarshall := json.Unmarshal(wrpModel.Payload, &d); errUnmarshall == nil {
			if 520 <= d.StatusCode && d.StatusCode <= 599 {
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
		"message": err.Error(),
	})

}

/* Request-type specific decoding functions */

func requestSetPayload(in io.Reader, newCID, oldCID, syncCMC string) (p []byte, err error) {
	var (
		wdmp = new(setWDMP)
		data []byte
	)

	if data, err = ioutil.ReadAll(in); err == nil {
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

	payload, err := ioutil.ReadAll(input)

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

	payload, err := ioutil.ReadAll(input)

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
