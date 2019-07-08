package common

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/comcast-bascule/bascule"

	"github.com/Comcast/webpa-common/logging"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
)

//HeaderWPATID is the header key for the WebPA transaction UUID
const HeaderWPATID = "X-WebPA-Transaction-Id"

//TransactionLogging is used by the different Tr1d1um services to
//keep track of incoming requests and their corresponding responses
func TransactionLogging(logger kitlog.Logger) kithttp.ServerFinalizerFunc {
	var infoLogger = logging.Info(logger)
	return func(ctx context.Context, code int, r *http.Request) {
		var satClientID = "N/A"

		// retrieve satClientID from request context
		if auth, ok := bascule.FromContext(r.Context()); ok {
			satClientID = auth.Token.Principal()
		}

		var rCtx = r.Context()

		transactionLogger := kitlog.WithPrefix(infoLogger,
			logging.MessageKey(), "Bookkeeping response",
			"requestAddress", r.RemoteAddr,
			"requestURLPath", r.URL.Path,
			"requestURLQuery", r.URL.RawQuery,
			"requestMethod", r.Method,
			"responseCode", code,
			"responseHeaders", ctx.Value(kithttp.ContextKeyResponseHeaders),
			"tid", ctx.Value(ContextKeyRequestTID),
			"satClientID", satClientID,
		)

		//For a request R, lantency includes time from points A to B where:
		//A: as soon as R is authorized
		//B: as soon as Tr1d1um is done sending the response for R
		var latency time.Duration

		if requestArrivalTime, ok := rCtx.Value(ContextKeyRequestArrivalTime).(time.Time); ok {
			latency = time.Since(requestArrivalTime)
		} else {
			logging.Error(logger).Log("tid", ctx.Value(ContextKeyRequestTID), logging.MessageKey(), "latency value could not be derived")
		}

		if WDMPBody, ok := ctx.Value(ContextKeyRequestWDMPBody).([]byte); ok {
			if wdmp, e := LoadWDMP(WDMPBody, r.Header.Get(HeaderWPASyncNewCID),
				r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC)); e == nil {
				transactionLogger = kitlog.WithPrefix(transactionLogger,
					"command", wdmp.Command,
					"parameters", getParamNames(wdmp.Parameters),
				)
			}
		}

		transactionLogger.Log("latency", latency)
	}
}

//ForwardHeadersByPrefix copies headers h where the source and target are 'from' and 'to' respectively such that key(h) has p as prefix
func ForwardHeadersByPrefix(p string, from http.Header, to http.Header) {
	for headerKey, headerValues := range from {
		if strings.HasPrefix(headerKey, p) {
			for _, headerValue := range headerValues {
				to.Add(headerKey, headerValue)
			}
		}
	}
}

//ErrorLogEncoder decorates the errorEncoder in such a way that
//errors are logged with their corresponding unique request identifier
func ErrorLogEncoder(logger kitlog.Logger, ee kithttp.ErrorEncoder) kithttp.ErrorEncoder {
	var errorLogger = logging.Error(logger)
	return func(ctx context.Context, e error, w http.ResponseWriter) {
		errorLogger.Log(logging.ErrorKey(), e.Error(), "tid", ctx.Value(ContextKeyRequestTID).(string))
		ee(ctx, e, w)
	}
}

//Welcome is an Alice-style constructor that defines necessary request
//context values assumed to exist by the delegate. These values should
//be those expected to be used both in and outside the gokit server flow
func Welcome(delegate http.Handler) http.Handler {
	return http.HandlerFunc(
		func(w http.ResponseWriter, r *http.Request) {
			var ctx = r.Context()
			ctx = context.WithValue(ctx, ContextKeyRequestArrivalTime, time.Now())
			delegate.ServeHTTP(w, r.WithContext(ctx))
		})
}

//Capture (for lack of a better name) captures context values of interest
//from the incoming request. Unlike Welcome, values captured here are
//intended to be used only throughout the gokit server flow: (request decoding, business logic,  response encoding)
func Capture(ctx context.Context, r *http.Request) (nctx context.Context) {
	var tid string
	if tid = r.Header.Get(HeaderWPATID); tid == "" {
		tid = genTID()
	}

	nctx = context.WithValue(ctx, ContextKeyRequestTID, tid)

	if r.Method == http.MethodPatch {
		bodyBytes, _ := ioutil.ReadAll(r.Body)
		r.Body.Close()

		r.Body = ioutil.NopCloser(bytes.NewBuffer(bodyBytes))

		nctx = context.WithValue(nctx, ContextKeyRequestWDMPBody, bodyBytes)
	}

	return
}

//genTID generates a 16-byte long string
//it returns "N/A" in the extreme case the random string could not be generated
func genTID() (tid string) {
	buf := make([]byte, 16)
	tid = "N/A"
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}
