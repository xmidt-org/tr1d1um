package common

import (
	"errors"
	"net/http"
)

//ErrTr1d1umInternal should be the error shown to external API consumers in Internal Server error cases
var ErrTr1d1umInternal = errors.New("oops! Something unexpected went wrong in this service")

//CodedError describes the behavior of an error that additionally has an HTTP status code used for TR1D1UM business logic
type CodedError interface {
	error
	StatusCode() int
}

type badRequestError struct {
	error
}

func (b *badRequestError) Error() string {
	return b.error.Error()
}

func (b *badRequestError) StatusCode() int {
	return http.StatusBadRequest
}

//NewBadRequestError is the constructor for an error returned for bad HTTP requests to tr1d1um
func NewBadRequestError(e error) CodedError {
	return &badRequestError{e}
}
