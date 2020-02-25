package translation

import (
	"bytes"

	"net/http"

	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/tr1d1um/common"

	"github.com/xmidt-org/wrp-go/wrp"
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

	//Acquirer provides a mechanism to build auth headers for outbound requests
	acquire.Acquirer

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
		Acquirer:          o.Acquirer,
	}
}

type service struct {
	common.Tr1d1umTransactor

	acquire.Acquirer

	XmidtWrpURL string

	WRPSource string
}

//SendWRP sends the given wrpMsg to the XMiDT cluster and returns the response if any
func (w *service) SendWRP(wrpMsg *wrp.Message, authHeaderValue string) (*common.XmidtResponse, error) {
	wrpMsg.Source = w.WRPSource

	var payload []byte

	err := wrp.NewEncoderBytes(&payload, wrp.Msgpack).Encode(wrpMsg)

	if err != nil {
		return nil, err
	}

	r, err := http.NewRequest(http.MethodPost, w.XmidtWrpURL, bytes.NewBuffer(payload))

	if err != nil {
		return nil, err
	}

	if w.Acquirer != nil {
		authHeaderValue, err = w.Acquirer.Acquire()
		if err != nil {
			return nil, err
		}
	}

	r.Header.Set("Content-Type", wrp.Msgpack.ContentType())
	r.Header.Set("Authorization", authHeaderValue)

	return w.Tr1d1umTransactor.Transact(r)
}
