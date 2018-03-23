package stat

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/webpa-common/device"

	"github.com/Comcast/tr1d1um/common"
	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
)

//Configs wraps the properties needed to set up the stat server
type Configs struct {
	S            Service
	R            *mux.Router
	Authenticate *alice.Chain
	Log          kitlog.Logger
}

//ConfigHandler sets up the server that powers the stat service
func ConfigHandler(c *Configs) {
	opts := []kithttp.ServerOption{
		kithttp.ServerErrorLogger(c.Log),
		kithttp.ServerErrorEncoder(encodeError),
		kithttp.ServerBefore(
			func(ctx context.Context, _ *http.Request) context.Context {
				return context.WithValue(ctx, common.ContextKeyRequestArrivalTime, time.Now())
			}),
		kithttp.ServerFinalizer(common.TransactionLogging(c.Log)),
	}

	statHandler := kithttp.NewServer(
		makeStatEndpoint(c.S),
		decodeRequest,
		encodeResponse,
		opts...,
	)

	c.R.Handle("/device/{deviceid}/stat", c.Authenticate.Then(statHandler)).
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

func encodeError(_ context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	switch {
	case err == device.ErrorInvalidDeviceName:
		w.WriteHeader(http.StatusBadRequest)
	case strings.Contains(err.Error(), "Client.Timeout exceeded"), err == context.Canceled || err == context.DeadlineExceeded:
		w.WriteHeader(http.StatusServiceUnavailable)

	default:
		w.WriteHeader(http.StatusInternalServerError)
		err = common.ErrTr1d1umInternal
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
