package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

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
