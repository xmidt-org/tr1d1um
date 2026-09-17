// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package stat

import (
	"context"
	"net/http"
	"strings"

	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/tr1d1um/transaction"
)

// Service defines the behavior of the device statistics Tr1d1um Service.
type Service interface {
	RequestStat(ctx context.Context, deviceID string) (*transaction.XmidtResponse, error)
}

// NewService constructs a new stat service instance given some options.
func NewService(o *ServiceOptions) Service {
	return &service{
		transactor:   o.HTTPTransactor,
		auth:         o.Auth,
		xmidtStatURL: o.XmidtStatURL,
	}
}

// ServiceOptions defines the options needed to build a new stat service.
type ServiceOptions struct {
	//Base Endpoint URL for device stats from the XMiDT API.
	//It's expected to have the "${device}" substring to perform device ID substitution.
	XmidtStatURL string

	//Auth decorates http requests with authorization header(s).
	//(Optional)
	Auth auth.Decorator

	//HTTPTransactor is the component that's responsible to make the HTTP
	//request to the XMiDT API and return only data we care about.
	HTTPTransactor transaction.T
}

type service struct {
	transactor transaction.T

	auth auth.Decorator

	xmidtStatURL string
}

// RequestStat contacts the XMiDT cluster for device statistics.
func (s *service) RequestStat(ctx context.Context, deviceID string) (*transaction.XmidtResponse, error) {
	r, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.Replace(s.xmidtStatURL, "${device}", deviceID, 1), nil)

	if err != nil {
		return nil, err
	}

	if s.auth != nil {
		err = s.auth.Decorate(ctx, r)
		if err != nil {
			return nil, err
		}
	}

	return s.transactor.Transact(r)
}
