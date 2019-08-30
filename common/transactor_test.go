package common

import (
	"bytes"
	"errors"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTransactError(t *testing.T) {
	assert := assert.New(t)

	plainErr := errors.New("network test error")
	expectedErr := NewCodedError(plainErr, 503)

	transactor := NewTr1d1umTransactor(&Tr1d1umTransactorOptions{
		Do: func(_ *http.Request) (*http.Response, error) {
			return nil, plainErr
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	_, e := transactor.Transact(r)

	assert.EqualValues(expectedErr, e)
}

func TestTransactIdeal(t *testing.T) {
	assert := assert.New(t)

	expected := &XmidtResponse{
		Code:             404,
		Body:             []byte("not found"),
		ForwardedHeaders: http.Header{"X-A": []string{"a", "b"}},
	}

	rawXmidtResponse := &http.Response{
		StatusCode: 404,
		Body:       ioutil.NopCloser(bytes.NewBufferString("not found")),
		Header: http.Header{
			"X-A": []string{"a", "b"}, //should be forwarded
			"Y-A": []string{"c", "d"}, //should be ignored
		},
	}

	transactor := NewTr1d1umTransactor(&Tr1d1umTransactorOptions{
		Do: func(_ *http.Request) (*http.Response, error) {
			return rawXmidtResponse, nil
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	actual, e := transactor.Transact(r)
	assert.Nil(e)
	assert.EqualValues(expected, actual)
}
