// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/xmidt-org/sallust"
)

func TestNewCodedError(t *testing.T) {
	assert := assert.New(t)
	var ce = NewCodedError(errors.New("test"), 500)
	assert.NotNil(ce)
	assert.Equal(500, ce.StatusCode())
	assert.Equal("test", ce.Error())
}

func TestBadRequestError(t *testing.T) {
	assert := assert.New(t)
	var ce = NewBadRequestError(errors.New("test"))
	assert.NotNil(ce)
	assert.Equal(400, ce.StatusCode())
	assert.Equal("test", ce.Error())
}

func TestErrorLogEncoder(t *testing.T) {
	tcs := []struct {
		desc      string
		getLogger func(context.Context) *zap.Logger
	}{
		{
			desc:      "nil getlogger",
			getLogger: nil,
		},
		{
			desc:      "valid getlogger",
			getLogger: sallust.Get,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			e := func(ctx context.Context, _ error, _ http.ResponseWriter) {
				assert.EqualValues("tid00", ctx.Value(ContextKeyRequestTID))
			}
			le := ErrorLogEncoder(tc.getLogger, e)

			if tc.getLogger == nil {
				assert.Panics(func() {
					// assumes TID is context
					le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
				})
			} else {
				assert.NotPanics(func() {
					// assumes TID is context
					le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
				})
			}
		})
	}
}

func TestErrorLogEncoder_Logging(t *testing.T) {
	notFound := NewCodedError(errors.New("device not found"), http.StatusNotFound)

	tests := []struct {
		name    string
		err     error
		wantLog bool
	}{
		{name: "plain error is a 500 and logged", err: errors.New("boom"), wantLog: true},
		{name: "coded 400 is logged", err: NewBadRequestError(errors.New("bad")), wantLog: true},
		{name: "coded 503 is logged", err: NewCodedError(errors.New("down"), http.StatusServiceUnavailable), wantLog: true},
		{name: "coded 404 is not logged", err: notFound},
		{name: "wrapped coded 404 is not logged", err: fmt.Errorf("wrapped: %w", notFound)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := context.WithValue(context.Background(), ContextKeyRequestTID, "tid00")
			rec := httptest.NewRecorder()

			var encoded error
			le := ErrorLogEncoder(
				func(context.Context) *zap.Logger { return zap.New(core) },
				func(_ context.Context, e error, w http.ResponseWriter) {
					encoded = e
					assert.Same(t, rec, w)
				},
			)
			le(ctx, tc.err, rec)

			assert.Equal(t, tc.err, encoded, "the error is always passed to the encoder")

			if !tc.wantLog {
				assert.Zero(t, logs.Len())
				return
			}

			require.Equal(t, 1, logs.Len())
			entry := logs.All()[0]
			assert.Equal(t, zapcore.ErrorLevel, entry.Level)
			assert.Equal(t, tc.err.Error(), entry.ContextMap()["error"])
			assert.Equal(t, "tid00", entry.ContextMap()["tid"])
		})
	}

	t.Run("no logger", func(t *testing.T) {
		var called bool
		le := ErrorLogEncoder(
			func(context.Context) *zap.Logger { return nil },
			func(context.Context, error, http.ResponseWriter) { called = true },
		)

		assert.NotPanics(t, func() { le(context.Background(), errors.New("boom"), httptest.NewRecorder()) })
		assert.True(t, called, "the error is encoded without a logger")
	})
}
