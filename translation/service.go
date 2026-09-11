// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"

	"net/http"

	"github.com/xmidt-org/bascule/acquire"
	"github.com/xmidt-org/tr1d1um/transaction"

	"github.com/xmidt-org/wrp-go/v3"
)

// Service represents the Webpa-Tr1d1um component that translates WDMP data into WRP
// which is compatible with the XMiDT API.
type Service interface {
	SendWRP(context.Context, *wrp.Message, string) (*transaction.XmidtResponse, error)
}

// ServiceOptions defines the options needed to build a new translation WRP service.
type ServiceOptions struct {
	//XmidtWrpURL is the URL of the XMiDT API which takes in WRP messages.
	XmidtWrpURL string

	//WRPSource is the value set on the WRPSource field of all WRP messages created by Tr1d1um.
	WRPSource string

	//Acquirer provides a mechanism to build auth headers for outbound requests.
	AuthAcquirer acquire.Acquirer

	//T is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about.
	transaction.T
}

// NewService constructs a new translation service instance given some options.
func NewService(o *ServiceOptions) Service {
	return &service{
		xmidtWrpURL:  o.XmidtWrpURL,
		wrpSource:    o.WRPSource,
		transactor:   o.T,
		authAcquirer: o.AuthAcquirer,
	}
}

type service struct {
	transactor   transaction.T
	authAcquirer acquire.Acquirer
	xmidtWrpURL  string
	wrpSource    string
}

// SendWRP sends the given wrpMsg to the XMiDT cluster and returns the response if any.
func (w *service) SendWRP(ctx context.Context, wrpMsg *wrp.Message, authHeaderValue string) (*transaction.XmidtResponse, error) {
	wrpMsg.Source = w.wrpSource

	var payload []byte

	err := wrp.NewEncoderBytes(&payload, wrp.Msgpack).Encode(wrpMsg)

	if err != nil {
		return nil, err
	}

	r, err := http.NewRequestWithContext(ctx, http.MethodPost, w.xmidtWrpURL, bytes.NewBuffer(payload))

	if err != nil {
		return nil, err
	}

	if w.authAcquirer != nil {
		authHeaderValue, err = w.authAcquirer.Acquire()
		if err != nil {
			return nil, err
		}
	}

	r.Header.Set("Content-Type", wrp.Msgpack.ContentType())
	r.Header.Set("Authorization", authHeaderValue)
	return w.transactor.Transact(r)
}
