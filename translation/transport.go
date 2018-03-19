package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/comcast/tr1d1um/common"
	"github.com/justinas/alice"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"

	"github.com/gorilla/mux"
)

// Error values
var (
	ErrEmptyNames     = errors.New("names parameter is required")
	ErrInvalidService = errors.New("unsupported Service")
	ErrInternal       = errors.New("oops! Something unexpected went wrong in our service")
)

const (
	applicationName, apiBase = "tr1d1um", "/api/v2"
	contentTypeHeaderKey     = "Content-Type"
	authHeaderKey            = "Authorization"
)

type TranslationOptions struct {
	S             Service
	R             *mux.Router
	Authenticate  *alice.Chain
	Log           kitlog.Logger
	ValidServices []string
}

//ConfigHandler sets up the handler for a given service
func ConfigHandler(t *TranslationOptions) {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorLogger(t.Log),
		kithttp.ServerErrorEncoder(encodeError),
		kithttp.ServerBefore(markArrivalTime),
		kithttp.ServerFinalizer(transactionLogging(t.Log)),
	}

	WRPHandler := kithttp.NewServer(
		makeTranslationEndpoint(t.S),
		decodeValidServiceRequest(t.ValidServices, decodeGetRequest),
		encodeResponse,
		opts...,
	)

	t.R.Handle("/device/{deviceid}/{service}", t.Authenticate.Then(WRPHandler)).Methods(http.MethodGet)
	return
}

/* Request Decoding */

func decodeGetRequest(c context.Context, r *http.Request) (getRequest interface{}, err error) {
	var (
		payload []byte
		wrpMsg  *wrp.Message
	)

	if payload, err = requestPayload(r.FormValue("names"), r.FormValue("attributes")); err == nil {
		if wrpMsg, err = wrapInWRP(payload, r.Header.Get(HeaderWPATID), mux.Vars(r)); err == nil {
			return &wrpRequest{
				WRPMessage: wrpMsg,
				AuthValue:  r.Header.Get(authHeaderKey),
			}, nil

		}
	}

	return
}

/* Response Encoding */

func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	resp := response.(*http.Response)
	var body []byte

	forwardHeadersByPrefix("X", resp, w)

	defer resp.Body.Close()
	if body, err = ioutil.ReadAll(resp.Body); err == nil {

		if resp.StatusCode != http.StatusOK { //just forward the XMiDT cluster response {
			w.WriteHeader(resp.StatusCode)
			_, err = w.Write(body)
			return
		}

		wrpModel := &wrp.Message{Type: wrp.SimpleRequestResponseMessageType}

		if err = wrp.NewDecoderBytes(body, wrp.Msgpack).Decode(wrpModel); err == nil {

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
	}

	return
}

/* Error Encoding */

func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set(contentTypeHeaderKey, "application/json")

	switch err {
	case ErrInvalidService:
		w.WriteHeader(http.StatusBadRequest)
	case context.DeadlineExceeded:
		w.WriteHeader(http.StatusServiceUnavailable)
	case ErrEmptyNames:
		w.WriteHeader(http.StatusBadRequest)
	default:
		w.WriteHeader(http.StatusInternalServerError)

		//todo: based on error logging go-kit timing, this is subject to change
		//idea is to prevent specific internal errors being shown to users (they are "internal" for a reason)
		err = ErrInternal
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"message": err.Error(),
	})

}

/* Helper methods */

func requestPayload(names, attributes string) ([]byte, error) {
	if names == "" {
		return nil, ErrEmptyNames
	}

	getWDMP := &GetWDMP{}

	//default values at this point
	getWDMP.Names, getWDMP.Command = strings.Split(names, ","), CommandGet

	if attributes != "" {
		getWDMP.Command, getWDMP.Attributes = CommandGetAttrs, attributes
	}

	return json.Marshal(getWDMP)
}

func wrapInWRP(WDMP []byte, tid string, pathVars map[string]string) (m *wrp.Message, err error) {
	var canonicalDeviceID device.ID

	if canonicalDeviceID, err = device.ParseID(pathVars["deviceid"]); err == nil {
		service := pathVars["service"]

		if tid == "" {
			if tid, err = genTID(); err != nil {
				return
			}
		}

		m = &wrp.Message{
			Type:            wrp.SimpleRequestResponseMessageType,
			Payload:         WDMP,
			Destination:     fmt.Sprintf("%s/%s", string(canonicalDeviceID), service),
			TransactionUUID: tid,
			Source:          service,
		}
	}
	return
}

func decodeValidServiceRequest(services []string, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
	return func(c context.Context, r *http.Request) (interface{}, error) {

		if vars := mux.Vars(r); vars == nil || !contains(vars["service"], services) {
			return nil, ErrInvalidService
		}

		return decoder(c, r)
	}
}

func forwardHeadersByPrefix(prefix string, resp *http.Response, w http.ResponseWriter) {
	if resp != nil {
		for headerKey, headerValues := range resp.Header {
			if strings.HasPrefix(headerKey, prefix) {
				for _, headerValue := range headerValues {
					w.Header().Add(headerKey, headerValue)
				}
			}
		}
	}
}

func contains(i string, elements []string) bool {
	for _, e := range elements {
		if e == i {
			return true
		}
	}
	return false
}

func markArrivalTime(ctx context.Context, _ *http.Request) context.Context {
	return context.WithValue(ctx, common.ContextKeyRequestArrivalTime, time.Now())
}

func transactionLogging(logger kitlog.Logger) kithttp.ServerFinalizerFunc {
	return func(ctx context.Context, code int, r *http.Request) {

		transactionLogger := kitlog.WithPrefix(logging.Info(logger),
			logging.MessageKey(), "Bookkeeping response",
			"requestAddress", r.RemoteAddr,
			"requestURLPath", r.URL.Path,
			"requestURLQuery", r.URL.RawQuery,
			"requestMethod", r.Method,
			"responseCode", code,
			"responseHeaders", ctx.Value(kithttp.ContextKeyResponseHeaders),
			"responseError", ctx.Value(common.ContextKeyResponseError),
		)

		var latency = "-"

		if requestArrivalTime, ok := ctx.Value(common.ContextKeyRequestArrivalTime).(time.Time); ok {
			latency = fmt.Sprintf("%v", time.Now().Sub(requestArrivalTime))
		} else {
			logging.Error(logger).Log(logging.ErrorKey(), "latency value could not be derived")
		}

		transactionLogger.Log("latency", latency)
	}
}
