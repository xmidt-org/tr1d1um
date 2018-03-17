package translation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/wrp"
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

	contentTypeHeaderKey = "Content-Type"
	authHeaderKey        = "Authorization"
	tidHeaderKey         = "X-WebPA-Transaction-Id"
)

type TranslationOptions struct {
	S             Service
	R             *mux.Router
	Authenticate  *alice.Chain
	Log           kitlog.Logger
	ValidServices []string
}

func ConfigHandler(t *TranslationOptions) {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorLogger(t.Log),
		kithttp.ServerErrorEncoder(encodeError),
	}

	WRPHandler := kithttp.NewServer(
		makeTranslationEndpoint(t.S),
		serviceDecorate(t.ValidServices, decodeGetRequest),
		encodeResponse,
		opts...,
	)

	t.R.Handle("/device/{deviceid}/{service}", t.Authenticate.Then(WRPHandler)).Methods(http.MethodGet)
	return
}

func decodeGetRequest(c context.Context, r *http.Request) (getRequest interface{}, err error) {
	var payload []byte

	if payload, err = requestPayload(r.FormValue("names"), r.FormValue("attributes")); err == nil {
		getRequest, err = wrapInWRP(payload, mux.Vars(r))
	}

	return
}

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

func wrapInWRP(WDMP []byte, pathVars map[string]string) (m *wrp.Message, err error) {
	var (
		tid string
		ok  bool
	)

	if tid, ok = pathVars[tidHeaderKey]; !ok {
		if tid, err = genTID(); err != nil {
			return
		}
	}

	deviceID, _ := pathVars["deviceid"]
	var canonicalDeviceID device.ID

	if canonicalDeviceID, err = device.ParseID(deviceID); err == nil {
		service, _ := pathVars["service"]

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

func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	resp := response.(*http.Response)

	defer resp.Body.Close()
	var body []byte

	forwardHeadersByPrefix("X", resp, w)

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
			if err = json.Unmarshal(wrpModel.Payload, &deviceResponseModel); err == nil {
				if deviceResponseModel.StatusCode != 0 && deviceResponseModel.StatusCode != http.StatusInternalServerError {
					w.WriteHeader(deviceResponseModel.StatusCode)
				}
			}

			_, err = w.Write(wrpModel.Payload)
		}
	}

	return
}

//encode errors from business logic
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

func serviceDecorate(services []string, decoder kithttp.DecodeRequestFunc) kithttp.DecodeRequestFunc {
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
