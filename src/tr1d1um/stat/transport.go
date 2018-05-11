package stat

import (
	"context"
	"encoding/json"
	"io/ioutil"
	"net/http"

	"tr1d1um/common"

	"github.com/Comcast/webpa-common/device"

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
//That is, it configures the mux paths to access the service
func ConfigHandler(c *Configs) {
	opts := []kithttp.ServerOption{
		kithttp.ServerBefore(common.Capture),
		kithttp.ServerErrorEncoder(common.ErrorLogEncoder(c.Log, encodeError)),
		kithttp.ServerFinalizer(common.TransactionLogging(c.Log)),
	}

	statHandler := kithttp.NewServer(
		makeStatEndpoint(c.S),
		decodeRequest,
		encodeResponse,
		opts...,
	)

	c.R.Handle("/device/{deviceid}/stat", c.Authenticate.Then(common.Welcome(statHandler))).
		Methods(http.MethodGet)
}

func decodeRequest(_ context.Context, r *http.Request) (req interface{}, err error) {
	var deviceID device.ID
	if deviceID, err = device.ParseID(mux.Vars(r)["deviceid"]); err == nil {
		req = &statRequest{
			AuthHeaderValue: r.Header.Get("Authorization"),
			DeviceID:        string(deviceID),
		}
	} else {
		err = common.NewBadRequestError(err)
	}

	return
}

func encodeError(ctx context.Context, err error, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")

	if ce, ok := err.(common.CodedError); ok {
		w.WriteHeader(ce.StatusCode())
	} else {
		w.WriteHeader(http.StatusInternalServerError)
		err = common.ErrTr1d1umInternal
	}

	json.NewEncoder(w).Encode(map[string]string{
		"message": err.Error(),
	})
}

//encodeResponse simply forwards the response Tr1d1um got from the XMiDT API
//TODO: What about if XMiDT cluster reports 500. There would be ambiguity
//about which machine is actually having the error (Tr1d1um or the Xmidt API)
//do we care to make that distinction?

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
