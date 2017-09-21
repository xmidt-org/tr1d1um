package main

import (
	"testing"
	"github.com/Comcast/webpa-common/wrp"
	"net/http/httptest"
	"net/http"
	"github.com/stretchr/testify/assert"
	"github.com/gorilla/mux"
	"errors"

	"gopkg.in/h2non/gock.v1"
	"time"
	"io"
)

func TestSend(t *testing.T){
	assert := assert.New(t)

	data := []byte("data")
	WRPMsg := &wrp.Message{}
	WRPPayload := []byte("payload")
	validURL := "http://someValidURL"

	tr1 := &Tr1SendAndHandle{log:&logTracker{}, timedClient: &http.Client{Timeout:time.Second}}
	tr1.NewHTTPRequest = http.NewRequest
	ch := &ConversionHandler{encodingHelper:mockEncoding, wdmpConvert:mockConversion, targetURL:validURL}

	t.Run("SendEncodeErr", func (t *testing.T){
		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWrp", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, errors.New(errMsg)).Once()

		recorder := httptest.NewRecorder()

		_, err := tr1.Send(ch, recorder, data, req)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
	})


	t.Run("SendNewRequestErr", func (t *testing.T){
		defer func() {
			tr1.NewHTTPRequest = http.NewRequest
		}()

		tr1.NewHTTPRequest = NewHTTPRequestFail

		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWrp", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()

		recorder := httptest.NewRecorder()
		_, err := tr1.Send(ch, recorder, data, req)

		assert.NotNil(err)
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)

	})

	t.Run("SendIdeal", func (t *testing.T) {
		defer gock.OffAll()

		req := httptest.NewRequest(http.MethodGet, validURL, nil)

		var URLVars Vars = mux.Vars(req)
		mockConversion.On("GetConfiguredWrp", data, URLVars, req.Header).Return(WRPMsg).Once()
		mockEncoding.On("GenericEncode", WRPMsg, wrp.JSON).Return(WRPPayload, nil).Once()

	 	gock.New(validURL).Reply(http.StatusOK)
		recorder := httptest.NewRecorder()

		_, err := tr1.Send(ch, recorder, data, req)

		assert.Nil(err)
		assert.EqualValues(http.StatusOK, recorder.Code)
		mockConversion.AssertExpectations(t)
		mockEncoding.AssertExpectations(t)
	})
}


func TestHandleResponse(t *testing.T){
	//Cases
	//incoming err

	//status not OK

	//extract payload fails

	//ideal

}

func NewHTTPRequestFail(_ ,_ string, _ io.Reader)(*http.Request,error) {
	return nil, errors.New(errMsg)
}

