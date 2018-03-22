package translation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
	"time"

	"github.com/Comcast/tr1d1um/common"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/justinas/alice"

	kitlog "github.com/go-kit/kit/log"
	kithttp "github.com/go-kit/kit/transport/http"

	"github.com/gorilla/mux"
)

// Error values
var (
	ErrEmptyNames        = errors.New("names parameter is required")
	ErrInvalidService    = errors.New("unsupported Service")
	ErrInternal          = errors.New("oops! Something unexpected went wrong in our service")
	ErrUnsupportedMethod = errors.New("unsupported method. Could not decode request payload")

	//Set command errors
	ErrInvalidSetWDMP = errors.New("invalid XPC SET message")
	ErrNewCIDRequired = errors.New("NewCid is required for TEST_AND_SET")

	//Add/Delete command  errors
	ErrMissingTable = errors.New("table property is required")
	ErrMissingRow   = errors.New("row property is required")

	//Replace command error
	ErrMissingRows = errors.New("rows property is required")
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
		kithttp.ServerBefore(func(ctx context.Context, _ *http.Request) context.Context {
			return context.WithValue(ctx, common.ContextKeyRequestArrivalTime, time.Now())
		}),
		kithttp.ServerFinalizer(transactionLogging(t.Log)),
	}

	WRPHandler := kithttp.NewServer(
		makeTranslationEndpoint(t.S),
		decodeValidServiceRequest(t.ValidServices, decodeRequest),
		encodeResponse,
		opts...,
	)

	t.R.Handle("/device/{deviceid}/{service}", t.Authenticate.Then(WRPHandler)).Methods(http.MethodGet)
	return
}

/* Request Decoding */

func decodeRequest(c context.Context, r *http.Request) (decodedRequest interface{}, err error) {
	var (
		payload []byte
		wrpMsg  *wrp.Message
	)

	if payload, err = requestPayload(r); err == nil {
		if wrpMsg, err = wrap(payload, r.Header.Get(HeaderWPATID), mux.Vars(r)); err == nil {
			decodedRequest = &wrpRequest{
				WRPMessage: wrpMsg,
				AuthValue:  r.Header.Get(authHeaderKey),
			}
		}
	}

	return
}

func requestPayload(r *http.Request) (payload []byte, err error) {

	switch r.Method {
	case http.MethodGet:
		payload, err = requestGetPayload(r.FormValue("names"), r.FormValue("attributes"))
	case http.MethodPatch:
		payload, err = requestSetPayload(r.Body, r.Header.Get(HeaderWPASyncNewCID), r.Header.Get(HeaderWPASyncOldCID), r.Header.Get(HeaderWPASyncCMC))

	default:
		//Unwanted methods should be filtered at the mux level. Thus, we should never get here
		err = ErrUnsupportedMethod
	}

	return
}

/* Response Encoding */

func encodeResponse(ctx context.Context, w http.ResponseWriter, response interface{}) (err error) {
	var (
		resp = response.(*http.Response)
		body []byte
	)

	forwardHeadersByPrefix("X", resp, w)

	defer resp.Body.Close()
	if body, err = ioutil.ReadAll(resp.Body); err == nil {

		if resp.StatusCode != http.StatusOK { //just forward the XMiDT cluster response {
			w.WriteHeader(resp.StatusCode)
			_, err = w.Write(body)
			return
		}

		wrpModel := new(wrp.Message)

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

/* Request-type specific decoding functions */

func requestSetPayload(in io.Reader, newCID, oldCID, syncCMC string) (p []byte, err error) {
	var (
		wdmp = new(setWDMP)
		data []byte
	)

	//read data into wdmp
	if data, err = ioutil.ReadAll(in); err == nil {
		if err = json.Unmarshal(data, wdmp); err == nil || len(data) == 0 {
			if err = validateAndDeduceSET(wdmp, newCID, oldCID, syncCMC); err == nil {
				return json.Marshal(wdmp)
			}
		}
	}

	return
}

func requestGetPayload(names, attributes string) ([]byte, error) {
	if names == "" {
		return nil, ErrEmptyNames
	}

	wdmp := new(getWDMP)

	//default values at this point
	wdmp.Names, wdmp.Command = strings.Split(names, ","), CommandGet

	if attributes != "" {
		wdmp.Command, wdmp.Attributes = CommandGetAttrs, attributes
	}

	return json.Marshal(wdmp)
}

func requestAddPayload(m map[string]string, input io.Reader) (p []byte, err error) {
	var wdmp = &addRowWDMP{Command: CommandAddRow}

	if table, ok := m["parameter"]; ok {
		wdmp.Table = table
	} else {
		return nil, ErrMissingTable
	}

	var payload []byte
	if payload, err = ioutil.ReadAll(input); err == nil {
		if len(payload) == 0 {
			return nil, ErrMissingRow
		}

		if err = json.Unmarshal(payload, &wdmp.Row); err == nil {
			return json.Marshal(wdmp)
		}
	}

	return
}

func requestReplacePayload(m map[string]string, input io.Reader) (p []byte, err error) {
	var wdmp = &replaceRowsWDMP{Command: CommandReplaceRows}

	if table, ok := m["parameter"]; ok {
		wdmp.Table = table
	} else {
		return nil, ErrMissingTable
	}

	var payload []byte
	if payload, err = ioutil.ReadAll(input); err == nil {
		if len(payload) == 0 {
			return nil, ErrMissingRows
		}

		if err = json.Unmarshal(payload, &wdmp.Rows); err == nil {
			return json.Marshal(wdmp)
		}
	}

	return
}
func requestDeletePayload(m map[string]string) ([]byte, error) {
	if row, ok := m["parameter"]; ok {
		return json.Marshal(&deleteRowDMP{Command: CommandDeleteRow, Row: row})
	}
	return nil, ErrMissingRow
}
