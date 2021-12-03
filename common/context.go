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
	"net"
	"net/http"
	"strings"
	"time"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/webpa-common/v2/logging"
)

type contextKey int

// Keys to important context values on incoming requests to TR1D1UM
const (
	ContextKeyRequestArrivalTime contextKey = iota
	ContextKeyRequestTID
	ContextKeyTransactionInfoLogger
)

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
// intended to be used only throughout the gokit server flow: (request decoding, business logic, response encoding)
func Capture(logger kitlog.Logger) kithttp.RequestFunc {
	var transactionInfoLogger = logging.Info(logger)
	return func(ctx context.Context, r *http.Request) (nctx context.Context) {
		var tid string

		if tid = r.Header.Get(transaction.HeaderWPATID); tid == "" {
			tid = genTID()
		}

		nctx = context.WithValue(ctx, ContextKeyRequestTID, tid)

		var satClientID = "N/A"

		// retrieve satClientID from request context
		if auth, ok := bascule.FromContext(r.Context()); ok {
			satClientID = auth.Token.Principal()
		}

		var source string
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			source = r.RemoteAddr
		} else {
			source = host
		}

		logKVs := []interface{}{logging.MessageKey(), "record",
			"request", transaction.transactionRequest{
				Address: source,
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

// ForwardHeadersByPrefix copies headers h where the source and target are 'from'
// and 'to' respectively such that key(h) has p as prefix
func ForwardHeadersByPrefix(p string, from http.Header, to http.Header) {
	for headerKey, headerValues := range from {
		if strings.HasPrefix(headerKey, p) {
			for _, headerValue := range headerValues {
				to.Add(headerKey, headerValue)
			}
		}
	}
}
