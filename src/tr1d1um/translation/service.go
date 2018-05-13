package translation

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"
	"tr1d1um/common"

	"github.com/Comcast/webpa-common/wrp"
)

//Service defines the behavior for the Translation WRP Service
type Service interface {
	SendWRP(*wrp.Message, string) (*http.Response, error)
}

//ServiceOptions defines the options needed to build a new translation WRP service
type ServiceOptions struct {
	//XmidtWrpURL is the URL of the XMiDT API which takes in WRP messages
	XmidtWrpURL string

	//Do is http.Client.Do with custom client configurations
	Do func(*http.Request) (*http.Response, error)

	//WRPSource is currently the prefix of "Source" field in outgoing WRP Messages
	WRPSource string

	//CtxTimeout is the timeout for any given HTTP transaction
	CtxTimeout time.Duration
}

//NewService constructs a new translation service instance given some options
func NewService(o *ServiceOptions) Service {
	return &service{
		Do:          o.Do,
		XmidtWrpURL: o.XmidtWrpURL,
		WRPSource:   o.WRPSource,
		CtxTimeout:  o.CtxTimeout,
	}
}

//service represents the Webpa-Tr1d1um component that translates WDMP data into WRP
//which is compatible with the XMiDT API
type service struct {
	Do func(*http.Request) (*http.Response, error)

	XmidtWrpURL string

	CtxTimeout time.Duration

	WRPSource string
}

//SendWRP sends the given wrpMsg to the XMiDT cluster and returns the *http.Response
func (w *service) SendWRP(wrpMsg *wrp.Message, authValue string) (resp *http.Response, err error) {
	var payload []byte

	// fill in the rest of the source property
	wrpMsg.Source = fmt.Sprintf("%s/%s", w.WRPSource, wrpMsg.Source)

	if err = wrp.NewEncoderBytes(&payload, wrp.Msgpack).Encode(wrpMsg); err == nil {
		var req *http.Request
		if req, err = http.NewRequest(http.MethodPost, w.XmidtWrpURL, bytes.NewBuffer(payload)); err == nil {

			req.Header.Add("Content-Type", wrp.Msgpack.ContentType())
			req.Header.Add("Authorization", authValue)

			ctx, cancel := context.WithTimeout(req.Context(), w.CtxTimeout)
			defer cancel()

			if resp, err = w.Do(req.WithContext(ctx)); err != nil {
				//Timeout, network errors, etc.
				err = common.NewCodedError(err, http.StatusServiceUnavailable)
			}
		}
	}
	return
}
