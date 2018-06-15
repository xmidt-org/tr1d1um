package translation

import (
	"bytes"
	"fmt"
	"net/http"
	"tr1d1um/common"

	"github.com/Comcast/webpa-common/wrp"
)

//Service represents the Webpa-Tr1d1um component that translates WDMP data into WRP
//which is compatible with the XMiDT API
type Service interface {
	SendWRP(*wrp.Message, string) (*common.XmidtResponse, error)
}

//ServiceOptions defines the options needed to build a new translation WRP service
type ServiceOptions struct {
	//XmidtWrpURL is the URL of the XMiDT API which takes in WRP messages
	XmidtWrpURL string

	//WRPSource is currently the prefix of "Source" field in outgoing WRP Messages
	WRPSource string

	//Tr1d1umTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about
	common.Tr1d1umTransactor
}

//NewService constructs a new translation service instance given some options
func NewService(o *ServiceOptions) Service {
	return &service{
		XmidtWrpURL:       o.XmidtWrpURL,
		WRPSource:         o.WRPSource,
		Tr1d1umTransactor: o.Tr1d1umTransactor,
	}
}

type service struct {
	common.Tr1d1umTransactor

	XmidtWrpURL string

	WRPSource string
}

//SendWRP sends the given wrpMsg to the XMiDT cluster and returns the response if any
func (w *service) SendWRP(wrpMsg *wrp.Message, authValue string) (result *common.XmidtResponse, err error) {
	var payload []byte

	// fill in the rest of the source property
	wrpMsg.Source = fmt.Sprintf("%s/%s", w.WRPSource, wrpMsg.Source)

	if err = wrp.NewEncoderBytes(&payload, wrp.Msgpack).Encode(wrpMsg); err == nil {
		var req *http.Request
		if req, err = http.NewRequest(http.MethodPost, w.XmidtWrpURL, bytes.NewBuffer(payload)); err == nil {

			req.Header.Add("Content-Type", wrp.Msgpack.ContentType())
			req.Header.Add("Authorization", authValue)

			result, err = w.Tr1d1umTransactor.Transact(req)
		}
	}
	return
}
