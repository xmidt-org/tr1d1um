// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type ctxKeyBefore struct{}

func TestHandler(t *testing.T) {
	errDecode := errors.New("decode")
	errServe := errors.New("serve")
	errEncode := errors.New("encode")

	tcs := []struct {
		desc         string
		decodeErr    error
		serveErr     error
		encodeErr    error
		expectedCode int
		expectedBody string
		expectedErr  error
		served       bool
	}{
		{
			desc:         "success",
			expectedCode: http.StatusCreated,
			expectedBody: "ok",
			served:       true,
		},
		{
			desc:         "decode error",
			decodeErr:    errDecode,
			expectedCode: http.StatusBadRequest,
			expectedErr:  errDecode,
		},
		{
			desc:         "serve error",
			serveErr:     errServe,
			expectedCode: http.StatusBadRequest,
			expectedErr:  errServe,
			served:       true,
		},
		{
			desc:         "encode error",
			encodeErr:    errEncode,
			expectedCode: http.StatusBadRequest,
			expectedErr:  errEncode,
			served:       true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			var (
				served      bool
				encodedErr  error
				finalCode   int
				finalHeader http.Header
				finalBefore any
				finalRan    bool
			)

			h := Handler[string, string]{
				Before: func(ctx context.Context, _ *http.Request) context.Context {
					return context.WithValue(ctx, ctxKeyBefore{}, "set")
				},
				Decode: func(ctx context.Context, _ *http.Request) (string, error) {
					assert.Equal("set", ctx.Value(ctxKeyBefore{}))
					return "request", tc.decodeErr
				},
				Serve: func(ctx context.Context, req string) (string, error) {
					assert.Equal("set", ctx.Value(ctxKeyBefore{}))
					assert.Equal("request", req)
					served = true
					return "ok", tc.serveErr
				},
				Encode: func(_ context.Context, w http.ResponseWriter, resp string) error {
					if tc.encodeErr != nil {
						return tc.encodeErr
					}
					w.Header().Set("X-Encoded", "yes")
					w.WriteHeader(http.StatusCreated)
					_, err := w.Write([]byte(resp))
					return err
				},
				EncodeError: func(_ context.Context, err error, w http.ResponseWriter) {
					encodedErr = err
					w.WriteHeader(http.StatusBadRequest)
				},
				Finalize: func(ctx context.Context, code int, _ *http.Request) {
					finalRan = true
					finalBefore = ctx.Value(ctxKeyBefore{})
					finalCode = code
					finalHeader, _ = ctx.Value(ContextKeyResponseHeaders).(http.Header)
				},
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

			assert.Equal(tc.served, served)
			assert.Equal(tc.expectedErr, encodedErr)
			assert.Equal(tc.expectedCode, rec.Code)
			assert.Equal(tc.expectedBody, rec.Body.String())

			// the finalizer always runs, sees the before-enriched context,
			// and gets the headers actually written
			require.True(finalRan)
			assert.Equal("set", finalBefore)
			assert.Equal(tc.expectedCode, finalCode)
			assert.Equal(rec.Header(), finalHeader)
		})
	}
}

// TestHandlerImplicitStatus checks that a response written without an
// explicit WriteHeader is reported to the finalizer as 200.
func TestHandlerImplicitStatus(t *testing.T) {
	assert := assert.New(t)

	var finalCode int
	h := Handler[struct{}, string]{
		Decode: func(context.Context, *http.Request) (struct{}, error) { return struct{}{}, nil },
		Serve:  func(context.Context, struct{}) (string, error) { return "body", nil },
		Encode: func(_ context.Context, w http.ResponseWriter, resp string) error {
			_, err := w.Write([]byte(resp))
			return err
		},
		EncodeError: func(context.Context, error, http.ResponseWriter) { assert.Fail("unexpected error") },
		Finalize:    func(_ context.Context, code int, _ *http.Request) { finalCode = code },
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(http.StatusOK, rec.Code)
	assert.Equal(http.StatusOK, finalCode)
	assert.Equal("body", rec.Body.String())
}

// TestHandlerNoFinalizer checks the handler works without the optional
// stages and leaves the writer unwrapped.
func TestHandlerNoFinalizer(t *testing.T) {
	assert := assert.New(t)

	h := Handler[struct{}, string]{
		Decode: func(context.Context, *http.Request) (struct{}, error) { return struct{}{}, nil },
		Serve:  func(context.Context, struct{}) (string, error) { return "body", nil },
		Encode: func(_ context.Context, w http.ResponseWriter, resp string) error {
			_, ok := w.(*statusRecorder)
			assert.False(ok)
			_, err := w.Write([]byte(resp))
			return err
		},
		EncodeError: func(context.Context, error, http.ResponseWriter) { assert.Fail("unexpected error") },
	}

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	assert.Equal(http.StatusOK, rec.Code)
	assert.Equal("body", rec.Body.String())
}

func TestStatusRecorderUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	s := &statusRecorder{ResponseWriter: rec}
	assert.Same(t, rec, s.Unwrap())
}

// TestHandlerClientClosedRequest checks that an error from any stage of a
// request whose context the server canceled, because the caller closed its
// connection, is reported as 499 rather than as whatever the error would
// otherwise map to.
func TestHandlerClientClosedRequest(t *testing.T) {
	errStage := errors.New("stage failed")
	coded := NewCodedError(errors.New("upstream down"), http.StatusServiceUnavailable)

	tcs := []struct {
		desc      string
		decodeErr error
		serveErr  error
		encodeErr error
	}{
		{desc: "decode", decodeErr: errStage},
		{desc: "serve", serveErr: errStage},
		{desc: "encode", encodeErr: errStage},
		{desc: "already coded serve error", serveErr: coded},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			var (
				encodedErr error
				finalCode  int
			)

			h := Handler[string, string]{
				Decode: func(context.Context, *http.Request) (string, error) { return "request", tc.decodeErr },
				Serve:  func(context.Context, string) (string, error) { return "ok", tc.serveErr },
				Encode: func(context.Context, http.ResponseWriter, string) error { return tc.encodeErr },
				EncodeError: func(_ context.Context, err error, w http.ResponseWriter) {
					encodedErr = err
					if ce, ok := errors.AsType[CodedError](err); ok {
						w.WriteHeader(ce.StatusCode())
						return
					}
					w.WriteHeader(http.StatusInternalServerError)
				},
				Finalize: func(_ context.Context, code int, _ *http.Request) { finalCode = code },
			}

			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx))

			require.Error(encodedErr)
			ce, ok := errors.AsType[CodedError](encodedErr)
			require.True(ok)
			assert.Equal(StatusClientClosedRequest, ce.StatusCode())
			require.ErrorIs(encodedErr, ErrClientClosedRequest)
			assert.Equal(StatusClientClosedRequest, rec.Code)
			assert.Equal(StatusClientClosedRequest, finalCode)

			// the stage's own error stays reachable, and visible in the message
			expectedCause := tc.decodeErr
			if expectedCause == nil {
				expectedCause = tc.serveErr
			}
			if expectedCause == nil {
				expectedCause = tc.encodeErr
			}
			require.ErrorIs(encodedErr, expectedCause)
			assert.Equal("client closed the request: "+expectedCause.Error(), encodedErr.Error())
		})
	}
}

// TestHandlerNotClientClosedRequest checks that only a canceled request
// context is reported as 499: a live context and a deadline both leave the
// stage's error alone.
func TestHandlerNotClientClosedRequest(t *testing.T) {
	errServe := errors.New("serve")

	expired, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()

	tcs := []struct {
		desc string
		ctx  context.Context
	}{
		{desc: "live context", ctx: context.Background()},
		{desc: "deadline exceeded", ctx: expired},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)

			var encodedErr error
			h := Handler[string, string]{
				Decode: func(context.Context, *http.Request) (string, error) { return "request", nil },
				Serve:  func(context.Context, string) (string, error) { return "", errServe },
				Encode: func(context.Context, http.ResponseWriter, string) error { return nil },
				EncodeError: func(_ context.Context, err error, w http.ResponseWriter) {
					encodedErr = err
					w.WriteHeader(http.StatusInternalServerError)
				},
			}

			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil).WithContext(tc.ctx))

			assert.Same(errServe, encodedErr)
			require.NotErrorIs(t, encodedErr, ErrClientClosedRequest)
			assert.Equal(http.StatusInternalServerError, rec.Code)
		})
	}
}
