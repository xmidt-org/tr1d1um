package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestWriteResponse(t *testing.T) {
	assert := assert.New(t)

	myMessage, statusCode, expectedBody := "RespMsg", 200, `{"message":"RespMsg"}`
	origin := httptest.NewRecorder()

	writeResponse(myMessage, statusCode, origin)

	assert.EqualValues(expectedBody, origin.Body.String())
	assert.EqualValues(200, origin.Code)
}

func TestReportError(t *testing.T) {
	t.Run("InternalErr", func(t *testing.T) {
		assert := assert.New(t)
		origin := httptest.NewRecorder()
		ReportError(errors.New("internal"), origin)

		assert.EqualValues(http.StatusInternalServerError, origin.Code)
		assert.EqualValues(`{"message":""}`, origin.Body.String())
	})

	t.Run("TimeoutErr", func(t *testing.T) {
		assert := assert.New(t)
		origin := httptest.NewRecorder()
		ReportError(context.Canceled, origin)

		assert.EqualValues(Tr1StatusTimeout, origin.Code)
		assert.EqualValues(`{"message":"Error Timeout"}`, origin.Body.String())
	})
}
