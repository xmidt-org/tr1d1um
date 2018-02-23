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

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Comcast/webpa-common/logging"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestSetUpHandler(t *testing.T) {
	logger := logging.DefaultLogger()

	t.Run("CompleteConfigSetUp", func(t *testing.T) {
		assert := assert.New(t)
		v := viper.New()
		v.Set("targetURL", "https://someCoolURL.com")
		v.SetDefault("clientTimeout", defaultClientTimeout)
		v.SetDefault("respWaitTimeout", defaultRespWaitTimeout)
		actualHandler := SetUpHandler(v, logger)

		AssertCommon(actualHandler, assert)
	})

	t.Run("IncompleteConfigSetup", func(t *testing.T) {
		assert := assert.New(t)
		v := viper.New()
		v.Set("targetURL", "https://someCoolURL.com")

		actualHandler := SetUpHandler(v, logger)

		AssertCommon(actualHandler, assert)
	})
}
func TestRouteConfigurations(t *testing.T) {
	r := mux.NewRouter()
	fakePreHandler := new(alice.Chain)
	fakeHandler := &ConversionHandler{}
	v := viper.New()
	v.Set("version", "v2")

	AddRoutes(r.PathPrefix("/api/v2").Subrouter(), fakePreHandler, fakeHandler)

	var nonEmpty bytes.Buffer
	nonEmpty.WriteString(`{empty: false}`)

	requests := []*http.Request{
		//0: case no base uri
		httptest.NewRequest(http.MethodGet, "http://someurl.com/", nil),

		//1: case method get but no service
		httptest.NewRequest(http.MethodGet, "http://server.com/api/v2/device/mac:11223344/", nil),

		//2: case method get normal
		httptest.NewRequest(http.MethodGet, "http://server.com/api/v2/device/mac:11223344/serv1", nil),

		//3: case method patch body is nil
		httptest.NewRequest(http.MethodPatch, "http://server.com/api/v2/device/mac:11223344/serv1", nil),

		//4: case method where body is nil
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1/param1", nil),

		//5: No parameter. Applicable to methods delete, put and post
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1", &nonEmpty),

		//6: Normal Case. Applicable to methods delete, put and post
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1/param",
			&nonEmpty),
	}

	expectedResults := map[int]bool{ //a map for reading ease with respect to ^
		0: false, 1: false, 2: true, 3: true, 4: false, 5: false, 6: true,
	}

	testsCases := make([]RouteTestBundle, len(requests))

	for i, request := range requests {
		testsCases[i] = RouteTestBundle{request, expectedResults[i]}
	}
	AssertConfiguredRoutes(r, t, testsCases)
}
func TestGetSupportedDevices(t *testing.T) {
	t.Run("TypicalCase", func(t *testing.T) {
		assert := assert.New(t)
		services := []string{"a", "b"}
		result := getSupportedServicesMap(services)
		assert.EqualValues(2, len(result))
		_, aExists := result["a"]
		_, bExists := result["b"]
		assert.True(aExists)
		assert.True(bExists)
	})

	t.Run("EdgeCases", func(t *testing.T) {
		assert := assert.New(t)
		resultFromNil := getSupportedServicesMap(nil)          // nil case
		resultFromEmpty := getSupportedServicesMap([]string{}) // empty list case
		assert.Empty(resultFromEmpty)
		assert.Empty(resultFromNil)
	})
}

//AssertConfiguredRoutes checks that all given tests cases pass with regards to requests that should be
//allowed to hit our handler
func AssertConfiguredRoutes(r *mux.Router, t *testing.T, testCases []RouteTestBundle) {
	for _, bundle := range testCases {
		var match mux.RouteMatch
		if bundle.passing != r.Match(bundle.req, &match) {
			fmt.Printf("Expecting request with URL='%s' and method='%s' to have a route?: %v but got %v",
				bundle.req.URL, bundle.req.Method, bundle.passing, !bundle.passing)
			t.FailNow()
		}
	}
}

func AssertCommon(actualHandler *ConversionHandler, assert *assert.Assertions) {
	assert.NotEmpty(actualHandler.TargetURL)
	assert.NotEmpty(actualHandler.WRPRequestURL)
	assert.NotNil(actualHandler.WdmpConvert)
	assert.NotNil(actualHandler.Sender)
	assert.NotNil(actualHandler.RequestValidator)
	assert.NotNil(actualHandler.RetryStrategy)
	assert.NotNil(actualHandler.Logger)
}

type RouteTestBundle struct {
	req     *http.Request
	passing bool
}
