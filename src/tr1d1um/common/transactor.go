package common

import (
	"context"
	"io/ioutil"
	"net/http"
	"time"
)

//XmidtResponse represents the data that a tr1d1um transactor keeps from an HTTP request to
//the XMiDT API
type XmidtResponse struct {

	//Code is the HTTP Status code received from the transaction
	Code int

	//ForwardedHeaders contains all the headers tr1d1um keeps from the transaction
	ForwardedHeaders http.Header

	//Body represents the full data off the XMiDT http.Response body
	Body []byte
}

//Tr1d1umTransactor performs a typical HTTP request but
//enforces some logic onto the HTTP transaction such as
//context-based timeout and header filtering
//this is a common utility for the stat and config tr1d1um services
type Tr1d1umTransactor interface {
	Transact(*http.Request) (*XmidtResponse, error)
}

//Tr1d1umTransactorOptions include parameters needed to configure the transactor
type Tr1d1umTransactorOptions struct {
	//RequestTimeout is the deadline duration for the HTTP transaction to be completed
	RequestTimeout time.Duration

	//Do is the core responsible to perform the actual HTTP request
	Do func(*http.Request) (*http.Response, error)
}

func NewTr1d1umTransactor(o *Tr1d1umTransactorOptions) Tr1d1umTransactor {
	return &tr1d1umTransactor{
		Do:             o.Do,
		RequestTimeout: o.RequestTimeout,
	}
}

type tr1d1umTransactor struct {
	RequestTimeout time.Duration
	Do             func(*http.Request) (*http.Response, error)
}

func (t *tr1d1umTransactor) Transact(req *http.Request) (result *XmidtResponse, err error) {
	ctx, cancel := context.WithTimeout(req.Context(), t.RequestTimeout)
	defer cancel()

	var resp *http.Response
	if resp, err = t.Do(req.WithContext(ctx)); err == nil {
		result = &XmidtResponse{
			ForwardedHeaders: make(http.Header),
			Body:             []byte{},
		}

		ForwardHeadersByPrefix("X", resp.Header, result.ForwardedHeaders)
		result.Code = resp.StatusCode

		defer resp.Body.Close()

		result.Body, err = ioutil.ReadAll(resp.Body)
		return
	}

	//Timeout, network errors, etc.
	err = NewCodedError(err, http.StatusServiceUnavailable)
	return
}
