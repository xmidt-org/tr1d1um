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
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/webpa-common/v2/basculechecks"
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
		kithttp.ServerErrorEncoder(transaction.ErrorLogEncoder(transaction.GetLogger, encodeError)),
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

/* Request Decoding */
func decodeRequest(ctx context.Context, r *http.Request) (decodedRequest interface{}, err error) {
	var (
		payload []byte
		wrpMsg  *wrp.Message
	)
	if payload, err = requestPayload(r); err == nil {
		var tid string
		ctxtid := ctx.Value(transaction.ContextKeyRequestTID)
		if ctxtid != nil {
			tid = ctxtid.(string)
		}

		partnerIDs := getPartnerIDsDecodeRequest(ctx, r)
		wrpMsg, err = wrap(payload, tid, mux.Vars(r), partnerIDs)
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
	var ctxKeyReqTID string
	c := ctx.Value(transaction.ContextKeyRequestTID)
	if c != nil {
		ctxKeyReqTID = c.(string)
	}
	w.Header().Set(candlelight.HeaderWPATIDKeyName, ctxKeyReqTID)

	if resp.Code != http.StatusOK { //just forward the XMiDT cluster response {
		w.WriteHeader(resp.Code)
		_, err = w.Write(resp.Body)
		return
	}

	wrpModel := new(wrp.Message)

	if err = wrp.NewDecoderBytes(resp.Body, wrp.Msgpack).Decode(wrpModel); err == nil {

		var deviceResponseModel struct {
			StatusCode int `json:"statusCode"`
		}

		w.Header().Set("Content-Type", "application/json; charset=utf-8")

		// if possible, use the device response status code
		if errUnmarshall := json.Unmarshal(wrpModel.Payload, &deviceResponseModel); errUnmarshall == nil {
			if deviceResponseModel.StatusCode != 0 && deviceResponseModel.StatusCode != http.StatusInternalServerError {
				w.WriteHeader(deviceResponseModel.StatusCode)
			}
		}

		_, err = w.Write(wrpModel.Payload)
	}

	return
}

/* Error Encoding */

func encodeError(ctx context.Context, err error, w http.ResponseWriter) {
	w.Header().Set(contentTypeHeaderKey, "application/json; charset=utf-8")
	var ctxKeyReqTID string
	c := ctx.Value(transaction.ContextKeyRequestTID)
	if c != nil {
		ctxKeyReqTID = c.(string)
	}

	w.Header().Set(candlelight.HeaderWPATIDKeyName, ctxKeyReqTID)
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
