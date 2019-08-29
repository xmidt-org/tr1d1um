package common

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/xmidt-org/bascule"

	"github.com/xmidt-org/webpa-common/logging"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
)

//HeaderWPATID is the header key for the WebPA transaction UUID
const HeaderWPATID = "X-WebPA-Transaction-Id"

//TransactionLogging is used by the different Tr1d1um services to
//keep track of incoming requests and their corresponding responses
func TransactionLogging(logger kitlog.Logger) kithttp.ServerFinalizerFunc {
	return func(ctx context.Context, code int, r *http.Request) {
		if transactionInfoLogger, ok := ctx.Value(ContextKeyTransactionInfoLogger).(kitlog.Logger); ok {
			transactionInfoLogger = kitlog.WithPrefix(transactionInfoLogger)

			latency, err := extractRequestArrivalTime(r.Context())

			if err != nil {
				tid, _ := ctx.Value(ContextKeyRequestTID).(string)
				logging.Error(logger).Log(logging.ErrorKey(), err, "tid", tid)
			}

			transactionInfoLogger.Log("latency", latency,
				"responseCode", code,
				"responseHeaders", ctx.Value(kithttp.ContextKeyResponseHeaders))
		}
	}
}

func extractRequestArrivalTime(ctx context.Context) (latency string, err error) {
	//For a request R, lantency includes time from points A to B where:
	//A: as soon as R is authorized
	//B: as soon as Tr1d1um is done sending the response for R
	latency = "N/A"
	if requestArrivalTime, ok := ctx.Value(ContextKeyRequestArrivalTime).(time.Time); ok {
		latency = time.Since(requestArrivalTime).String()
	} else {
		err = errors.New("Request arrival time was not capture in go-kit context")
	}

	return
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

		transactionInfoLogger := kitlog.WithPrefix(transactionInfoLogger,
			logging.MessageKey(), "Bookkeeping response",
			"requestAddress", r.RemoteAddr,
			"requestURLPath", r.URL.Path,
			"requestURLQuery", r.URL.RawQuery,
			"requestMethod", r.Method,
			"tid", tid,
			"satClientID", satClientID,
		)

		return context.WithValue(nctx, ContextKeyTransactionInfoLogger, transactionInfoLogger)
	}
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
