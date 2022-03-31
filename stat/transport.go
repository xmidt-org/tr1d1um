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

package stat

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/transaction"
	"go.uber.org/zap"

	"github.com/xmidt-org/webpa-common/v2/device"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

// Options wraps the properties needed to set up the stat server
type Options struct {
	S Service

	//APIRouter is assumed to be a subrouter with the API prefix path (i.e. 'api/v2')
	APIRouter                   *mux.Router
	Authenticate                *alice.Chain
	Log                         *zap.Logger
	ReducedLoggingResponseCodes []int
}

// ConfigHandler sets up the server that powers the stat service
// That is, it configures the mux paths to access the service
func ConfigHandler(c *Options) {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorEncoder(transaction.ErrorLogEncoder(transaction.GetLogger, encodeError)),
		kithttp.ServerFinalizer(transaction.Log(c.Log, c.ReducedLoggingResponseCodes)),
	}

	statHandler := kithttp.NewServer(
		makeStatEndpoint(c.S),
		decodeRequest,
		encodeResponse,
		opts...,
	)

	c.APIRouter.Handle("/device/{deviceid}/stat", c.Authenticate.Then(transaction.Welcome(statHandler))).
		Methods(http.MethodGet)
}

func decodeRequest(_ context.Context, r *http.Request) (req interface{}, err error) {
	var deviceID device.ID
	if deviceID, err = device.ParseID(mux.Vars(r)["deviceid"]); err == nil {
		req = &statRequest{
			AuthHeaderValue: r.Header.Get("Authorization"),
			DeviceID:        string(deviceID),
		}
	} else {
		err = transaction.NewBadRequestError(err)
	}

	return
}

func encodeError(ctx context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set(candlelight.HeaderWPATIDKeyName, ctx.Value(transaction.ContextKeyRequestTID).(string))
	var ce transaction.CodedError
	if errors.As(err, &ce) {
		// if ce, ok := err.(transaction.CodedError); ok {
		w.WriteHeader(ce.StatusCode())
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		err = transaction.ErrTr1d1umInternal
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": err.Error(),
	})
}

// encodeResponse simply forwards the response Tr1d1um got from the XMiDT API
// TODO: What about if XMiDT cluster reports 500. There would be ambiguity
// about which machine is actually having the error (Tr1d1um or the Xmidt API)
// do we care to make that distinction?
func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	resp := response.(*transaction.XmidtResponse)

	if resp.Code == http.StatusOK {
		w.Header().Set("Content-Type", "application/json")
	} else {
		w.Header().Del("Content-Type")
	}

	w.Header().Set(candlelight.HeaderWPATIDKeyName, ctx.Value(transaction.ContextKeyRequestTID).(string))
	transaction.ForwardHeadersByPrefix("", resp.ForwardedHeaders, w.Header())

	w.WriteHeader(resp.Code)
	_, err = w.Write(resp.Body)
	return
}
