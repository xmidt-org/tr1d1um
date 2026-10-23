// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBodyLimit(t *testing.T) {
	var (
		seen   string
		served bool
	)
	var readErr error
	handler := BodyLimit(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		served = true
		var body []byte
		body, readErr = io.ReadAll(r.Body)
		seen = string(body)
		w.WriteHeader(http.StatusOK)
	}))

	serve := func(body string, contentLength int64) *httptest.ResponseRecorder {
		served, seen = false, ""
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.ContentLength = contentLength
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	t.Run("under the limit passes through intact", func(t *testing.T) {
		rec := serve("0123456789", 10)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, served)
		require.NoError(t, readErr)
		assert.Equal(t, "0123456789", seen, "the handler reads the whole body")
	})

	t.Run("declared length over the limit is refused unread", func(t *testing.T) {
		rec := serve("01234567890", 11)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.False(t, served)
		assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
		assert.JSONEq(t, `{"message":"request body too large"}`, rec.Body.String())
	})

	t.Run("undeclared length over the limit is refused", func(t *testing.T) {
		rec := serve("01234567890", -1)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
		assert.False(t, served)
	})

	t.Run("no body passes through", func(t *testing.T) {
		served = false
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.True(t, served)
	})

	t.Run("an unreadable body reaches the handler", func(t *testing.T) {
		var got error
		h := BodyLimit(10)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, got = io.ReadAll(r.Body)
			w.WriteHeader(http.StatusBadRequest)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", failingReader{})
		req.ContentLength = -1
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Error(t, got)
	})

	t.Run("zero is the default", func(t *testing.T) {
		h := BodyLimit(0)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		}))
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", DefaultMaxRequestBodyBytes)))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)

		req = httptest.NewRequest(http.MethodPost, "/", strings.NewReader(strings.Repeat("a", DefaultMaxRequestBodyBytes+1)))
		rec = httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	})
}

// failingReader fails every read, like a connection that dropped.
type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }
