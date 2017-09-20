package main

import (
	"errors"
	"github.com/Comcast/webpa-common/logging"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
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

var FailingEncode = func(_ interface{}, _ wrp.Format) ([]byte, error) {
	return nil, errors.New(errMsg)
}

var SucceedingEncode = func(_ interface{}, _ wrp.Format) ([]byte, error) {
	return nil, nil
}

func TestConversionTHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := &ConversionHandler{errorLogger: &fakeLogger}

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
		commonRequest := httptest.NewRequest(http.MethodGet, "http://device/config?", nil)
		SetUp(false, ch)

		//force GetFlavorFormat to succeed
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) (*GetWDMP, error) {
			return nil, nil
		}

		ch.SendRequest = func(handler *ConversionHandler, writer http.ResponseWriter, message *wrp.Message) {}

		recorder := httptest.NewRecorder()
		ch.ConversionHandler(recorder, commonRequest)

		assert.EqualValues(1, len(fakeLogger.vals))
		assert.EqualValues(1, len(fakeLogger.keys))
		assert.EqualValues(http.StatusInternalServerError, recorder.Code)
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[0])
		assert.EqualValues(errMsg, fakeLogger.vals[0])
	})

	//todo: more tests per case but only for ideal cases as the errors above are common to all commands
}

func SetUp(success bool, ch *ConversionHandler) {
	if success {
		ch.GenericEncode = SucceedingEncode
	} else {
		ch.GenericEncode = FailingEncode
	}
}
