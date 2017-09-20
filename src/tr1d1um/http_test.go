package main

import (
	"errors"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

const errMsg = "shared failure"

type logTracker struct {
	keys []interface{}
	vals []interface{}
}

func (fake *logTracker) Log(keyVals ...interface{}) (err error) {
	for i, keyVal := range keyVals {
		if i%2 == 0 {
			fake.keys = append(fake.keys, keyVal)
		} else {
			fake.vals = append(fake.vals, keyVal)
		}
	}
	return
}

func (fake *logTracker) Reset() {
	fake.vals = nil
	fake.keys = nil
}

var FailingEncode = func(_ interface{}) ([]byte, error) {
	return nil, errors.New(errMsg)
}

func TestConversionHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := &ConversionHandler{errorLogger: &fakeLogger}

	resultCatcher := &Catcher{}
	ch.EncodeJson = resultCatcher.CatchResult
	ch.SendRequest = resultCatcher.InterceptRequest

	t.Run("ErrDataParse", func(testing *testing.T) {
		defer fakeLogger.Reset()
		commonRequest := httptest.NewRequest(http.MethodGet, "http://device/config?", nil)

		//force GetFlavorFormat to fail
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) (*GetWDMP, error) {
			return nil, errors.New(errMsg)
		}

		recorder := httptest.NewRecorder()
		ch.ConversionHandler(recorder, commonRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("ErrEncode", func(testing *testing.T) {
		defer fakeLogger.Reset()

		ch.EncodeJson = FailingEncode
		commonRequest := httptest.NewRequest(http.MethodGet, "http://device/config?", nil)

		//force GetFlavorFormat to succeed
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) (*GetWDMP, error) {
			return nil, nil
		}

		recorder := httptest.NewRecorder()
		ch.ConversionHandler(recorder, commonRequest)

		assert.EqualValues(1, len(fakeLogger.vals))
		assert.EqualValues(1, len(fakeLogger.keys))
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[0])
		assert.EqualValues(errMsg, fakeLogger.vals[0])

		ch.EncodeJson = resultCatcher.CatchResult
	})

	t.Run("IdealGet", func(t *testing.T) {
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) (*GetWDMP, error) {
			return wdmpGet, nil
		}

		AssertCommon(ch, http.MethodGet, wdmpGet, resultCatcher, assert)
	})

	t.Run("IdealSet", func(t *testing.T) {
		ch.SetFlavorFormat = func(_ *http.Request) (*SetWDMP, error) {
			return wdmpSet, nil
		}

		AssertCommon(ch, http.MethodPatch, wdmpSet, resultCatcher, assert)
	})

	t.Run("IdealAdd", func(t *testing.T) {
		ch.AddFlavorFormat = func(_ io.Reader, _ Vars, _ string) (*AddRowWDMP, error) {
			return wdmpAdd, nil
		}

		AssertCommon(ch, http.MethodPost, wdmpAdd, resultCatcher, assert)
	})

	t.Run("IdealReplace", func(t *testing.T) {
		ch.ReplaceFlavorFormat = func(_ io.Reader, _ Vars, _ string) (*ReplaceRowsWDMP, error) {
			return wdmpReplace, nil
		}

		AssertCommon(ch, http.MethodPut, wdmpReplace, resultCatcher, assert)
	})

	t.Run("IdealDelete", func(t *testing.T) {
		ch.DeleteFlavorFormat = func(_ Vars, _ string) (*DeleteRowWDMP, error) {
			return wdmpDel, nil
		}

		AssertCommon(ch, http.MethodDelete, wdmpDel, resultCatcher, assert)
	})
}

func AssertCommon(ch *ConversionHandler, method string, expected interface{}, c *Catcher, assert *assert.Assertions) {
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(method, "http://someurl", nil)
	ch.ConversionHandler(recorder, req)
	assert.EqualValues(expected, c.LasResult)
	assert.True(c.SendRequestCalled)

	//reset values
	c.SendRequestCalled = false
	c.LasResult = nil
}

type Catcher struct {
	LasResult         interface{}
	SendRequestCalled bool
}

func (catcher *Catcher) CatchResult(v interface{}) ([]byte, error) {
	catcher.LasResult = v
	return nil, nil
}

func (catcher *Catcher) InterceptRequest(_ *ConversionHandler, _ http.ResponseWriter, _ *wrp.Message) {
	catcher.SendRequestCalled = true
}
