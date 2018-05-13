package common

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Comcast/webpa-common/logging"

	"github.com/stretchr/testify/assert"
)

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)

		to := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		ForwardHeadersByPrefix("H", resp, to)
		assert.Empty(to.Header())
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		resp, to := &http.Response{Header: http.Header{}}, httptest.NewRecorder()

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("He", resp, to)
		assert.NotEmpty(to.Header())
		assert.EqualValues(1, len(to.Header()))
		assert.EqualValues("3", to.Header().Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)
		to := httptest.NewRecorder()
		resp := &http.Response{Header: http.Header{}}

		resp.Header.Add("Helium", "3")
		resp.Header.Add("Hydrogen", "5")
		resp.Header.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("H", resp, to)
		assert.NotEmpty(to.Header)
		assert.EqualValues(2, len(to.Header()))
		assert.EqualValues([]string{"5", "6"}, to.Header()["Hydrogen"])
	})

	t.Run("NilCases", func(t *testing.T) {
		to, resp := httptest.NewRecorder(), &http.Response{}
		//none of these should panic
		ForwardHeadersByPrefix("", nil, nil)
		ForwardHeadersByPrefix("", resp, to)
	})
}

func TestErrorLogEncoder(t *testing.T) {
	assert := assert.New(t)
	e := func(ctx context.Context, _ error, _ http.ResponseWriter) {
		assert.EqualValues("tid00", ctx.Value(ContextKeyRequestTID))
	}
	le := ErrorLogEncoder(logging.DefaultLogger(), e)

	assert.NotPanics(func() {
		//assumes TID is context
		le(context.WithValue(context.TODO(), ContextKeyRequestTID, "tid00"), errors.New("test"), nil)
	})
}

func TestWelcome(t *testing.T) {
	assert := assert.New(t)
	var handler = http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		assert.NotNil(r.Context().Value(ContextKeyRequestArrivalTime))
	})

	decorated := Welcome(handler)
	req := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
	decorated.ServeHTTP(nil, req)
}

func TestCapture(t *testing.T) {
	t.Run("GivenTID", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		r.Header.Set(HeaderWPATID, "tid01")
		ctx := Capture(context.TODO(), r)
		assert.EqualValues("tid01", ctx.Value(ContextKeyRequestTID).(string))
	})

	t.Run("GeneratedTID", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		ctx := Capture(context.TODO(), r)
		assert.NotEmpty(ctx.Value(ContextKeyRequestTID).(string))
	})
}

func TestGenTID(t *testing.T) {
	assert := assert.New(t)
	tid := genTID()
	assert.NotEmpty(tid)
}
