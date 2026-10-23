// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"go.uber.org/zap"
)

// ErrTr1d1umInternal should be the error shown to external API consumers in Internal Server error cases
var ErrTr1d1umInternal = errors.New("oops! Something unexpected went wrong in this service")

// StatusClientClosedRequest is the status recorded when the caller closed
// its connection before tr1d1um finished the request.  It follows the nginx
// convention; no caller ever receives it, since the caller is gone.  It
// keeps client cancellations out of the 5xx counts in logs and metrics.
const StatusClientClosedRequest = 499

// ErrClientClosedRequest wraps the error a stage returned because the
// caller's request context was canceled.
var ErrClientClosedRequest = errors.New("client closed the request")

// ErrXmidtUnavailable is what a caller is told when the XMiDT cluster could
// not be reached or did not answer in time.  The cause, which names the
// cluster's address, is logged but never sent to the caller.
var ErrXmidtUnavailable = errors.New("the XMiDT cluster could not be reached")

// CodedError describes the behavior of an error that additionally has an HTTP status code used for TR1D1UM business logic
type CodedError interface {
	error
	StatusCode() int
}

// CallerError is an error whose full text is for the log and whose
// CallerMessage is for the caller, because the full text names something
// internal, such as the address of the XMiDT cluster.
type CallerError interface {
	error
	CallerMessage() string
}

type codedError struct {
	error
	statusCode int

	// callerMessage, when set, is what the caller is told instead of the
	// error text.
	callerMessage string
}

func (c *codedError) StatusCode() int {
	return c.statusCode
}

// CallerMessage implements CallerError.  It is the message set for the
// caller, or the wrapped error's when that is a CallerError, or else the
// error text.
func (c *codedError) CallerMessage() string {
	if c.callerMessage != "" {
		return c.callerMessage
	}

	if inner, ok := errors.AsType[CallerError](c.error); ok {
		return inner.CallerMessage()
	}

	return c.Error()
}

// Unwrap exposes the underlying error, so errors.Is and errors.As see
// through the status code to the cause.
func (c *codedError) Unwrap() error {
	return c.error
}

// CallerMessage returns what a caller should be told about err: its
// CallerMessage when it is a CallerError, otherwise its text.
func CallerMessage(err error) string {
	if ce, ok := errors.AsType[CallerError](err); ok {
		return ce.CallerMessage()
	}

	return err.Error()
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

// NewXmidtUnavailableError returns the 503 for a request XMiDT could not
// serve: cause is for the log, and the caller is told only
// ErrXmidtUnavailable.
func NewXmidtUnavailableError(cause error) CodedError {
	return &codedError{
		error:         fmt.Errorf("%w: %w", ErrXmidtUnavailable, cause),
		statusCode:    http.StatusServiceUnavailable,
		callerMessage: ErrXmidtUnavailable.Error(),
	}
}

// ErrorLogEncoder decorates the errorEncoder in such a way that
// errors are logged with their corresponding unique request identifier
func ErrorLogEncoder(getLogger func(context.Context) *zap.Logger, ee ErrorEncoder) ErrorEncoder {
	return func(ctx context.Context, e error, w http.ResponseWriter) {
		code := http.StatusInternalServerError
		if ce, ok := errors.AsType[CodedError](e); ok {
			code = ce.StatusCode()
		}

		if l := getLogger(ctx); l != nil && code != http.StatusNotFound {
			l.Error("sending non-200, non-404 response", zap.String("error", e.Error()),
				zap.Any("tid", ctx.Value(ContextKeyRequestTID)),
			)
		}

		ee(ctx, e, w)
	}
}
