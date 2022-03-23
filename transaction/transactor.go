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
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"io/ioutil"
	"net"
	"net/http"
	"strings"
	"time"

	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/candlelight"
	"go.uber.org/zap"
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

// T performs a typical HTTP request but
// enforces some logic onto the HTTP transaction such as
// context-based timeout and header filtering
// this is a common utility for the stat and config tr1d1um services
type T interface {
	Transact(*http.Request) (*XmidtResponse, error)
}

// Options include parameters needed to configure the transactor
type Options struct {
	//RequestTimeout is the deadline duration for the HTTP transaction to be completed
	RequestTimeout time.Duration

	//Do is the core responsible to perform the actual HTTP request
	Do func(*http.Request) (*http.Response, error)
}

type transactor struct {
	RequestTimeout time.Duration
	Do             func(*http.Request) (*http.Response, error)
}

type Request struct {
	Address string `json:"address,omitempty"`
	Path    string `json:"path,omitempty"`
	Query   string `json:"query,omitempty"`
	Method  string `json:"method,omitempty"`
}

type response struct {
	Code    int         `json:"code,omitempty"`
	Headers interface{} `json:"headers,omitempty"`
}

func (re *Request) MarshalJSON() ([]byte, error) {
	return json.Marshal(re)
}

func (rs *response) MarshalJSON() ([]byte, error) {
	return json.Marshal(rs)
}

func New(o *Options) T {
	return &transactor{
		Do:             o.Do,
		RequestTimeout: o.RequestTimeout,
	}
}

func (t *transactor) Transact(req *http.Request) (result *XmidtResponse, err error) {
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

// Log is used by the different Tr1d1um services to
// keep track of incoming requests and their corresponding responses
func Log(logger *zap.Logger, reducedLoggingResponseCodes []int) kithttp.ServerFinalizerFunc {
	return func(ctx context.Context, code int, r *http.Request) {
		tid, _ := ctx.Value(ContextKeyRequestTID).(string)
		transactionLogger, transactionLoggerOk := ctx.Value(ContextKeyTransactionLogger).(*zap.Logger)

		if !transactionLoggerOk {
			traceID, spanID, ok := candlelight.ExtractTraceInfo(ctx)
			if !ok {
				logger.Error("transaction logger not found in context", zap.String("tid", tid))
			} else {
				logger.Error("transaction logger not found in context", zap.String("tid", tid), zap.String(candlelight.TraceIdLogKeyName, traceID), zap.String(candlelight.SpanIDLogKeyName, spanID))
			}
			return
		}

		requestArrival, ok := ctx.Value(ContextKeyRequestArrivalTime).(time.Time)

		if !ok {
			transactionLogger = transactionLogger.With(zap.Reflect("duration", time.Since(requestArrival)))
		} else {
			traceID, spanID, ok := candlelight.ExtractTraceInfo(ctx)
			if !ok {
				logger.Error("Request arrival not capture for transaction logger", zap.String("tid", tid))
			} else {
				logger.Error("Request arrival not capture for transaction logger", zap.String("tid", tid), zap.String(candlelight.TraceIdLogKeyName, traceID), zap.String(candlelight.SpanIDLogKeyName, spanID))
			}
		}

		includeHeaders := true
		response := response{Code: code}

		for _, responseCode := range reducedLoggingResponseCodes {
			if responseCode == code {
				includeHeaders = false
				break
			}
		}

		if includeHeaders {
			response.Headers = ctx.Value(kithttp.ContextKeyResponseHeaders)
		}

		transactionLogger.Info("response", zap.Reflect("response", response))
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
func Capture(logger *zap.Logger) kithttp.RequestFunc {
	return func(ctx context.Context, r *http.Request) (nctx context.Context) {
		var tid string

		if tid = r.Header.Get(HeaderWPATID); tid == "" {
			tid = GenTID()
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

		transactionLogger := logger.With(
			zap.Reflect("request", Request{
				Address: source,
				Path:    r.URL.Path,
				Query:   r.URL.RawQuery,
				Method:  r.Method},
			),
			zap.String("tid", tid),
			zap.String("satClientID", satClientID),
		)
		traceID, spanID, ok := candlelight.ExtractTraceInfo(ctx)
		if ok {
			transactionLogger = transactionLogger.With(
				zap.String(candlelight.TraceIdLogKeyName, traceID),
				zap.String(candlelight.SpanIDLogKeyName, spanID),
			)
		}
		return context.WithValue(nctx, ContextKeyTransactionLogger, transactionLogger)
	}
}

// GenTID generates a 16-byte long string
// it returns "N/A" in the extreme case the random string could not be generated
func GenTID() (tid string) {
	buf := make([]byte, 16)
	tid = "N/A"
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}
