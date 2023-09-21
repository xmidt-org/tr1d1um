// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"errors"
	"net/http"

	kithttp "github.com/go-kit/kit/transport/http"
	"go.uber.org/zap"
)

// ErrTr1d1umInternal should be the error shown to external API consumers in Internal Server error cases
var ErrTr1d1umInternal = errors.New("oops! Something unexpected went wrong in this service")

// CodedError describes the behavior of an error that additionally has an HTTP status code used for TR1D1UM business logic
type CodedError interface {
	error
	StatusCode() int
}

type codedError struct {
	error
	statusCode int
}

func (c *codedError) StatusCode() int {
	return c.statusCode
}

// NewBadRequestError is the constructor for an error returned for bad HTTP requests to tr1d1um
func NewBadRequestError(e error) CodedError {
	return NewCodedError(e, http.StatusBadRequest)
}

// NewCodedError upgrades an Error to a CodedError
// e must not be non-nil to avoid panics
func NewCodedError(e error, code int) CodedError {
	return &codedError{
		error:      e,
		statusCode: code,
	}
}

// ErrorLogEncoder decorates the errorEncoder in such a way that
// errors are logged with their corresponding unique request identifier
func ErrorLogEncoder(getLogger func(context.Context) *zap.Logger, ee kithttp.ErrorEncoder) kithttp.ErrorEncoder {
	return func(ctx context.Context, e error, w http.ResponseWriter) {
		code := http.StatusInternalServerError
		var sc kithttp.StatusCoder
		if errors.As(e, &sc) {
			code = sc.StatusCode()
		}

		if l := getLogger(ctx); l != nil && code != http.StatusNotFound {
			l.Error("sending non-200, non-404 response", zap.String("error", e.Error()),
				zap.Any("tid", ctx.Value(ContextKeyRequestTID)),
			)
		}

		ee(ctx, e, w)
	}
}
