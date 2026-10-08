// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

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
				finalCtx    context.Context
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
					finalCtx = ctx
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
			require.NotNil(finalCtx)
			assert.Equal("set", finalCtx.Value(ctxKeyBefore{}))
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
