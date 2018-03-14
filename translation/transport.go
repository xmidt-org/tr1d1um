package translation

import (
	"context"
	"encoding/json"
	"errors"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/justinas/alice"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"

	"github.com/gorilla/mux"
)

var (
	ErrEmptyNames     = errors.New("names parameter is required to be valid")
	ErrInvalidService = errors.New("Unsupported Service")
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
		decodeValidServiceRequests(t.ValidServices),
		encodeResponse,
		opts...,
	)

	t.R.Handle("/device/{deviceid}/{service}", t.Authenticate.Then(WRPHandler)).Methods(http.MethodGet)
	return
}

func decodeValidServiceRequests(services []string) kithttp.DecodeRequestFunc {
	return func(_ context.Context, r *http.Request) (getRequest interface{}, err error) {

		if vars := mux.Vars(r); vars == nil || !contains(vars["service"], services) {
			return nil, ErrInvalidService
		}

		var getWDMP struct {
			Command    string   `json:"command"`
			Names      []string `json:"names"`
			Attributes string   `json:"attributes,omitemtpy"`
		}

		if names := r.FormValue("names"); names != "" {
			getWDMP.Names = strings.Split(names, ",")

			if attributes := r.FormValue("attributes"); attributes != "" {
				getWDMP.Command, getWDMP.Attributes = CommandGetAttrs, attributes
			} else {
				getWDMP.Command = CommandGet
			}
		} else {
			return nil, ErrEmptyNames
		}

		var payload []byte

		if payload, err = json.Marshal(getWDMP); err == nil {
			var (
				wrpMsg *wrp.Message
				tid    string
			)

			if tid = r.Header.Get(tidHeaderKey); tid == "" {
				if tid, err = genTID(); err != nil {
					return
				}
			}

			if wrpMsg, err = configWRP(payload, r, tid); err == nil {
				getRequest = &wrpRequest{
					WRPMessage: wrpMsg,
					AuthValue:  r.Header.Get(authHeaderKey),
				}
			}
		}
		return
	}
}

func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	resp := response.(*http.Response)

	defer resp.Body.Close()
	var body []byte

	if body, err = ioutil.ReadAll(resp.Body); err == nil {

		if resp.StatusCode != http.StatusOK { //just forward the XMiDT cluster response {
			//todo
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
			_, err = w.Write(wrpModel.Payload)

			// if possible, use the device response status code
			if err = json.Unmarshal(wrpModel.Payload, &deviceResponseModel); err == nil {
				if deviceResponseModel.StatusCode != 0 && deviceResponseModel.StatusCode != http.StatusInternalServerError {
					w.WriteHeader(deviceResponseModel.StatusCode)
				}
			}
		}
	}
	return
}

func contains(i string, elements []string) bool {
	for _, e := range elements {
		if e == i {
			return true
		}
	}
	return false
}

//encode errors from business logic
func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set(contentTypeHeaderKey, "application/json")
	switch err {
	default:
		w.WriteHeader(http.StatusInternalServerError)
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"error": err.Error(),
	})

}
