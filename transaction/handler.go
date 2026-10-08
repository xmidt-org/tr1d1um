// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"net/http"
)

// RequestFunc runs before the request is decoded and may enrich the
// context, for example with request-scoped log fields.
type RequestFunc func(context.Context, *http.Request) context.Context

// ErrorEncoder writes an error from any stage of the handler to the
// response.
type ErrorEncoder func(context.Context, error, http.ResponseWriter)

// FinalizerFunc runs once the response has been written.  The context
// carries the response headers under ContextKeyResponseHeaders.
type FinalizerFunc func(ctx context.Context, code int, r *http.Request)

// Handler is the decode → service → encode pipeline shared by the device
// routes.  Each stage is a plain function; any error short-circuits to
// EncodeError.  Before and Finalize are optional.
type Handler[Req, Resp any] struct {
	// Before enriches the context before decoding.
	Before RequestFunc

	// Decode turns the HTTP request into the service request.
	Decode func(context.Context, *http.Request) (Req, error)

	// Serve performs the request.
	Serve func(context.Context, Req) (Resp, error)

	// Encode writes the service response.
	Encode func(context.Context, http.ResponseWriter, Resp) error

	// EncodeError writes any error from Decode, Serve or Encode.
	EncodeError ErrorEncoder

	// Finalize observes the outcome after the response is written.
	Finalize FinalizerFunc
}

// ServeHTTP implements http.Handler.
func (h Handler[Req, Resp]) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if h.Finalize != nil {
		rw := &statusRecorder{ResponseWriter: w, code: http.StatusOK}
		defer func() {
			h.Finalize(context.WithValue(ctx, ContextKeyResponseHeaders, rw.Header()), rw.code, r)
		}()
		w = rw
	}

	if h.Before != nil {
		ctx = h.Before(ctx, r)
	}

	req, err := h.Decode(ctx, r)
	if err != nil {
		h.EncodeError(ctx, err, w)
		return
	}

	resp, err := h.Serve(ctx, req)
	if err != nil {
		h.EncodeError(ctx, err, w)
		return
	}

	if err := h.Encode(ctx, w, resp); err != nil {
		h.EncodeError(ctx, err, w)
	}
}

// statusRecorder remembers the status code written so the finalizer can
// log it.  An implicit WriteHeader counts as 200.
type statusRecorder struct {
	http.ResponseWriter
	code int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.code = code
	s.ResponseWriter.WriteHeader(code)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (s *statusRecorder) Unwrap() http.ResponseWriter {
	return s.ResponseWriter
}
