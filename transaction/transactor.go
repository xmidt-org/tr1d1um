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

package transaction

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

// HeaderWPATID is the header key for the WebPA transaction UUID
const HeaderWPATID = "X-WebPA-Transaction-Id"

// XmidtResponse represents the data that a tr1d1um transactor keeps from an HTTP request to
// the XMiDT API
type XmidtResponse struct {

	//Code is the HTTP Status code received from the transaction
	Code int

	//ForwardedHeaders contains all the headers tr1d1um keeps from the transaction
	ForwardedHeaders http.Header

	//Body represents the full data off the XMiDT http.Response body
	Body []byte
}

// Tr1d1umTransactor performs a typical HTTP request but
// enforces some logic onto the HTTP transaction such as
// context-based timeout and header filtering
// this is a common utility for the stat and config tr1d1um services
type Tr1d1umTransactor interface {
	Transact(*http.Request) (*XmidtResponse, error)
}

// Tr1d1umTransactorOptions include parameters needed to configure the transactor
type Tr1d1umTransactorOptions struct {
	//RequestTimeout is the deadline duration for the HTTP transaction to be completed
	RequestTimeout time.Duration

	//Do is the core responsible to perform the actual HTTP request
	Do func(*http.Request) (*http.Response, error)
}

type tr1d1umTransactor struct {
	RequestTimeout time.Duration
	Do             func(*http.Request) (*http.Response, error)
}

type transactionRequest struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
	Method  string `json:"method,omitempty"`
}

type transactionResponse struct {
	Code    int         `json:"code,omitempty"`
	Headers interface{} `json:"headers,omitempty"`
}

func (re *transactionRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(re)
}

func (rs *transactionResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(rs)
}

func NewTr1d1umTransactor(o *Tr1d1umTransactorOptions) Tr1d1umTransactor {
	return &tr1d1umTransactor{
		Do:             o.Do,
		RequestTimeout: o.RequestTimeout,
	}
}

func (t *tr1d1umTransactor) Transact(req *http.Request) (result *XmidtResponse, err error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.RequestTimeout)
	defer cancel()

	var resp *http.Response
	if resp, err = t.Do(req.WithContext(ctx)); err == nil {
		result = &XmidtResponse{
			ForwardedHeaders: make(http.Header),
			Body:             []byte{},
		}

		ForwardHeadersByPrefix("X", resp.Header, result.ForwardedHeaders)
		result.Code = resp.StatusCode

		defer resp.Body.Close()

		result.Body, err = ioutil.ReadAll(resp.Body)
		return
	}

	//Timeout, network errors, etc.
	err = NewCodedError(err, http.StatusServiceUnavailable)
	return
}

// TransactionLogging is used by the different Tr1d1um services to
// keep track of incoming requests and their corresponding responses
func TransactionLogging(reducedLoggingResponseCodes []int, logger kitlog.Logger) kithttp.ServerFinalizerFunc {
	errorLogger := logging.Error(logger)
	return func(ctx context.Context, code int, r *http.Request) {
		tid, _ := ctx.Value(ContextKeyRequestTID).(string)
		transactionInfoLogger, transactionLoggerOk := ctx.Value(ContextKeyTransactionInfoLogger).(kitlog.Logger)

		if !transactionLoggerOk {
			var kvs = []interface{}{logging.MessageKey(), "transaction logger not found in context", "tid", tid}
			kvs, _ = candlelight.AppendTraceInfo(r.Context(), kvs)
			errorLogger.Log(kvs...)
			return
		}

		requestArrival, ok := ctx.Value(ContextKeyRequestArrivalTime).(time.Time)

		if ok {
			transactionInfoLogger = kitlog.WithPrefix(transactionInfoLogger, "duration", time.Since(requestArrival))
		} else {
			kvs := []interface{}{logging.ErrorKey(), "Request arrival not capture for transaction logger", "tid", tid}
			kvs, _ = candlelight.AppendTraceInfo(r.Context(), kvs)
			errorLogger.Log(kvs...)
		}

		includeHeaders := true
		response := transactionResponse{Code: code}

		for _, responseCode := range reducedLoggingResponseCodes {
			if responseCode == code {
				includeHeaders = false
				break
			}
		}

		if includeHeaders {
			response.Headers = ctx.Value(kithttp.ContextKeyResponseHeaders)
		}

		transactionInfoLogger.Log("response", response)
	}
}
