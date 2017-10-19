package main

import (
	"bytes"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"time"

	"github.com/Comcast/webpa-common/logging"
	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
)

func TestSetUpHandler(t *testing.T) {
	assert := assert.New(t)
	v := viper.New()
	v.Set("targetURL", "https://someCoolURL.com")
	v.SetDefault("clientTimeout", defaultClientTimeout)
	v.SetDefault("respWaitTimeout", defaultRespWaitTimeout)

	logger := logging.DefaultLogger()

	t.Run("NormalSetUp", func(t *testing.T) {
		actualHandler := SetUpHandler(v, logger)
		AssertCommon(actualHandler, v, assert)
	})

	t.Run("IncompleteConfig", func(t *testing.T) {
		actualHandler := SetUpHandler(v, logger)
		realSender := actualHandler.sender.(*Tr1SendAndHandle)

		//turn to default values
		assert.EqualValues(time.Second*40, realSender.respTimeout)
		assert.EqualValues(time.Second*30, realSender.client.Timeout)
		AssertCommon(actualHandler, v, assert)
	})
}

func TestRouteConfigurations(t *testing.T) {
	r := mux.NewRouter()
	fakePreHandler := new(alice.Chain)
	fakeHandler := &ConversionHandler{}
	v := viper.New()
	v.Set("version", "v2")

	AddRoutes(r, fakePreHandler, fakeHandler, v)

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

		//4: case method (put | post) body is nil
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1/param1", nil),

		//5: No parameter. Applicable to methods delete, put and post
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1", &nonEmpty),

		//6: Normal Case. Applicable to methods delete, put and post
		httptest.NewRequest(http.MethodPost, "http://server.com/api/v2/device/mac:11223344/serv1/param",
			&nonEmpty),
	}

	expectedResults := map[int]bool{ //a map for reading ease with respect to ^
		0: false, 1: false, 2: true, 3: false, 4: false, 5: false, 6: true,
	}

	testsCases := make([]RouteTestBundle, len(requests))

	for i, request := range requests {
		testsCases[i] = RouteTestBundle{request, expectedResults[i]}
	}
	AssertConfiguredRoutes(r, t, testsCases)
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

func AssertCommon(actualHandler *ConversionHandler, v *viper.Viper, assert *assert.Assertions) {
	assert.NotNil(actualHandler.wdmpConvert)
	assert.NotNil(actualHandler.encodingHelper)
	assert.NotNil(actualHandler.logger)
	assert.EqualValues(v.Get("targetURL"), actualHandler.targetURL)
	assert.NotNil(actualHandler.sender)

	realizedSender := actualHandler.sender.(*Tr1SendAndHandle)

	//assert necessary inner methods are set in the method under test
	assert.NotNil(realizedSender.client)
	assert.NotNil(realizedSender.NewHTTPRequest)
}

type RouteTestBundle struct {
	req     *http.Request
	passing bool
}
