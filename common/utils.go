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

package common

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/xmidt-org/candlelight"

	"github.com/xmidt-org/bascule"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

type transactionRequest struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
	Method  string `json:"method,omitempty"`
}

func (re *transactionRequest) MarshalJSON() ([]byte, error) {
	return json.Marshal(re)
}

type transactionResponse struct {
	Code    int         `json:"code,omitempty"`
	Headers interface{} `json:"headers,omitempty"`
}

func (rs *transactionResponse) MarshalJSON() ([]byte, error) {
	return json.Marshal(rs)
}

// HeaderWPATID is the header key for the WebPA transaction UUID
const HeaderWPATID = "X-WebPA-Transaction-Id"

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
			errorLogger.Log(kvs)
			return
		}

		requestArrival, ok := ctx.Value(ContextKeyRequestArrivalTime).(time.Time)

		if ok {
			transactionInfoLogger = kitlog.WithPrefix(transactionInfoLogger, "duration", time.Since(requestArrival))
		} else {
			kvs := []interface{}{logging.ErrorKey(), "Request arrival not capture for transaction logger", "tid", tid}
			kvs, _ = candlelight.AppendTraceInfo(r.Context(), kvs)
			errorLogger.Log(kvs)
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

// ForwardHeadersByPrefix copies headers h where the source and target are 'from' and 'to' respectively such that key(h) has p as prefix
func ForwardHeadersByPrefix(p string, from http.Header, to http.Header) {
	for headerKey, headerValues := range from {
		if strings.HasPrefix(headerKey, p) {
			for _, headerValue := range headerValues {
				to.Add(headerKey, headerValue)
			}
		}
	}
}

// ErrorLogEncoder decorates the errorEncoder in such a way that
// errors are logged with their corresponding unique request identifier
func ErrorLogEncoder(logger kitlog.Logger, ee kithttp.ErrorEncoder) kithttp.ErrorEncoder {
	var errorLogger = logging.Error(logger)
	return func(ctx context.Context, e error, w http.ResponseWriter) {
		errorLogger.Log(logging.ErrorKey(), e.Error(), "tid", ctx.Value(ContextKeyRequestTID).(string))
		ee(ctx, e, w)
	}
}

// Welcome is an Alice-style constructor that defines necessary request
// context values assumed to exist by the delegate. These values should
// be those expected to be used both in and outside the gokit server flow
func Welcome(delegate http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var ctx = r.Context()
			ctx = context.WithValue(ctx, ContextKeyRequestArrivalTime, time.Now())
			delegate.ServeHTTP(w, r.WithContext(ctx))
		})
}

// Capture (for lack of a better name) captures context values of interest
// from the incoming request. Unlike Welcome, values captured here are
// intended to be used only throughout the gokit server flow: (request decoding, business logic,  response encoding)
func Capture(logger kitlog.Logger) kithttp.RequestFunc {
	var transactionInfoLogger = logging.Info(logger)
	return func(ctx context.Context, r *http.Request) (nctx context.Context) {
		var tid string

		if tid = r.Header.Get(HeaderWPATID); tid == "" {
			tid = genTID()
		}

		nctx = context.WithValue(ctx, ContextKeyRequestTID, tid)

		var satClientID = "N/A"

		// retrieve satClientID from request context
		if auth, ok := bascule.FromContext(r.Context()); ok {
			satClientID = auth.Token.Principal()
		}

		u, err := url.Parse(r.RemoteAddr)
		if err != nil {
			//what should I do here?
		}

		logKVs := []interface{}{logging.MessageKey(), "record",
			"request", transactionRequest{
				Address: u.Hostname(),
				Path:    r.URL.Path,
				Query:   r.URL.RawQuery,
				Method:  r.Method,
			},
			"tid", tid,
			"satClientID", satClientID,
		}

		logKVs, _ = candlelight.AppendTraceInfo(ctx, logKVs)
		transactionInfoLogger := kitlog.WithPrefix(transactionInfoLogger, logKVs...)
		return context.WithValue(nctx, ContextKeyTransactionInfoLogger, transactionInfoLogger)
	}
}

// genTID generates a 16-byte long string
// it returns "N/A" in the extreme case the random string could not be generated
func genTID() (tid string) {
	buf := make([]byte, 16)
	tid = "N/A"
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}
