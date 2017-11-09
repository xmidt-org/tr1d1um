/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */
package main

/*
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
		assert.False(ShouldRetryOnError(errors.New("internal"), origin, true))

		assert.EqualValues(http.StatusInternalServerError, origin.Code)
		assert.EqualValues(`{"message":""}`, origin.Body.String())
	})

	t.Run("TimeoutErr", func(t *testing.T) {
		assert := assert.New(t)
		timeoutErrors := []error{context.Canceled, context.DeadlineExceeded, errors.New("error!: Client.Timeout exceeded")}

		for _, timeoutError := range timeoutErrors {
			origin := httptest.NewRecorder()
			ReportError(timeoutError, origin)
			assert.EqualValues(Tr1StatusTimeout, origin.Code)
			assert.EqualValues(`{"message":"Error Timeout"}`, origin.Body.String())
		}
	})

	t.Run("NilError", func(t *testing.T) {
		assert := assert.New(t)

		origin := httptest.NewRecorder()
		ReportError(nil, origin)
		assert.EqualValues(http.StatusOK, origin.Code) //assert for default value
	})
}

func TestGetStatusCodeFromRDKResponse(t *testing.T) {
	t.Run("IdealRDKResponse", func(t *testing.T) {
		assert := assert.New(t)

		RDKResponse := []byte(`{"statusCode": 200}`)
		statusCode, err := GetStatusCodeFromRDKResponse(RDKResponse)
		assert.EqualValues(200, statusCode)
		assert.Nil(err)
	})

	t.Run("InvalidRDKResponse", func(t *testing.T) {
		assert := assert.New(t)

		statusCode, err := GetStatusCodeFromRDKResponse(nil)
		assert.EqualValues(500, statusCode)
		assert.NotNil(err)
	})
	t.Run("RDKResponseNoStatusCode", func(t *testing.T) {
		assert := assert.New(t)

		RDKResponse := []byte(`{"something": "irrelevant"}`)
		statusCode, err := GetStatusCodeFromRDKResponse(RDKResponse)
		assert.EqualValues(500, statusCode)
		assert.NotNil(err)
	})
}
*/
