package stat

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/Comcast/webpa-common/device"

	"github.com/Comcast/tr1d1um/common"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

type StatOptions struct {
	S            Service
	R            *mux.Router
	Authenticate *alice.Chain
	Log          kitlog.Logger
}

func ConfigHandler(o *StatOptions) {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorLogger(o.Log),
		kithttp.ServerErrorEncoder(encodeError),
		kithttp.ServerBefore(
			func(ctx context.Context, _ *http.Request) context.Context {
				return context.WithValue(ctx, common.ContextKeyRequestArrivalTime, time.Now())
			}),
		kithttp.ServerFinalizer(common.TransactionLogging(o.Log)),
	}

	statHandler := kithttp.NewServer(
		makeStatEndpoint(o.S),
		decodeRequest,
		encodeResponse,
		opts...,
	)

	o.R.Handle("/device/{deviceid}/stat", o.Authenticate.Then(statHandler)).
		Methods(http.MethodGet)
}

func decodeRequest(_ context.Context, r *http.Request) (req interface{}, err error) {
	if _, err = device.ParseID(mux.Vars(r)["deviceid"]); err == nil {
		req = &statRequest{
			AuthValue: r.Header.Get("Authorization"),
			URI:       r.URL.RequestURI(),
		}
	}
	return
}

//TODO: need to capture http.Client.Timeout errors
func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case err == device.ErrorInvalidDeviceName:
		w.WriteHeader(http.StatusBadRequest)
	case err == context.Canceled || err == context.DeadlineExceeded:
		w.WriteHeader(http.StatusServiceUnavailable)

	default:
		w.WriteHeader(http.StatusInternalServerError)
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": err.Error(),
	})
}

//encodeResponse simply forwards
//TODO: this needs revision. What about if XMiDT cluster reports 500. There would be ambiguity
//about which machine is actually having the error

func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	resp := response.(*http.Response)

	var rp []byte
	if rp, err = ioutil.ReadAll(resp.Body); err == nil {
		w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
		w.WriteHeader(resp.StatusCode)
		w.Write(rp)
	}
	return
}
