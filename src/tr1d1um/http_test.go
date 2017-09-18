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
	"time"
)

const errMsg = "shared failure"

var expectedPayload = []byte{'_'}

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

func TestConversionGETHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := ConversionHandler{errorLogger: &fakeLogger}
	const errMsg = "getFlavorFormat failed"
	var expectedPayload = []byte{'_'}

	fakeRequest := httptest.NewRequest(http.MethodGet, "http://device/config?", nil)
	t.Run("GetFlavorFormatErr", func(testing *testing.T) {
		//force GetFlavorFormat to fail
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) ([]byte, error) {
			return nil, errors.New(errMsg)
		}

		ch.ConversionGETHandler(nil, fakeRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("NoError", func(testing *testing.T) {
		//fake success and changes in method
		ch.GetFlavorFormat = func(_ *http.Request, _ string, _ string, _ string) ([]byte, error) {
			return expectedPayload, nil
		}

		var actualPayload []byte

		// Set SendData
		ch.SendData = func(duration time.Duration, writer http.ResponseWriter, response *wrp.SimpleRequestResponse) {
			actualPayload = response.Payload
		}

		ch.ConversionGETHandler(nil, fakeRequest)
		assert.EqualValues(expectedPayload, actualPayload)
	})
}

func TestConversionSETHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := ConversionHandler{errorLogger: &fakeLogger}
	fakeRequest := httptest.NewRequest(http.MethodPatch, "http://device/config?", nil)

	t.Run("SetFlavorFormatErr", func(testing *testing.T) {
		ch.SetFlavorFormat = func(_ *http.Request, _ BodyReader) ([]byte, error) {
			return nil, errors.New(errMsg)
		}

		ch.ConversionSETHandler(nil, fakeRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("SetFlavorNoError", func(testing *testing.T) {
		ch.SetFlavorFormat = func(_ *http.Request, _ BodyReader) ([]byte, error) {
			return expectedPayload, nil
		}

		var actualPayload []byte

		ch.SendData = func(_ time.Duration, _ http.ResponseWriter, response *wrp.SimpleRequestResponse) {
			actualPayload = response.Payload
		}

		ch.ConversionSETHandler(nil, fakeRequest)
		assert.EqualValues(expectedPayload, actualPayload)
	})

}

func TestConversionDELETEHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := ConversionHandler{errorLogger: &fakeLogger}
	fakeRequest := httptest.NewRequest(http.MethodDelete, "http://device/config?", nil)

	t.Run("DeleteFlavorFormatErr", func(testing *testing.T) {
		ch.DeleteFlavorFormat = func(vars Vars, i string) ([]byte, error) {
			return nil, errors.New(errMsg)
		}

		ch.ConversionDELETEHandler(nil, fakeRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("DeleteFlavorNoError", func(testing *testing.T) {
		ch.DeleteFlavorFormat = func(vars Vars, i string) ([]byte, error) {
			return expectedPayload, nil
		}

		var actualPayload []byte

		ch.SendData = func(_ time.Duration, _ http.ResponseWriter, response *wrp.SimpleRequestResponse) {
			actualPayload = response.Payload
		}

		ch.ConversionDELETEHandler(nil, fakeRequest)
		assert.EqualValues(expectedPayload, actualPayload)
	})
}

func TestConversionREPLACEHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := ConversionHandler{errorLogger: &fakeLogger}
	fakeRequest := httptest.NewRequest(http.MethodPut, "http://device/config?", nil)

	t.Run("ReplaceFlavorFormatErr", func(testing *testing.T) {
		ch.ReplaceFlavorFormat = func(_ io.Reader, _ Vars, _ string, _ BodyReader) ([]byte, error) {
			return nil, errors.New(errMsg)
		}

		ch.ConversionREPLACEHandler(nil, fakeRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("ReplaceFlavorNoError", func(testing *testing.T) {
		ch.ReplaceFlavorFormat = func(_ io.Reader, _ Vars, _ string, _ BodyReader) ([]byte, error) {
			return expectedPayload, nil
		}

		var actualPayload []byte

		ch.SendData = func(_ time.Duration, _ http.ResponseWriter, response *wrp.SimpleRequestResponse) {
			actualPayload = response.Payload
		}

		ch.ConversionREPLACEHandler(nil, fakeRequest)
		assert.EqualValues(expectedPayload, actualPayload)
	})
}

func TestConversionADDHandler(t *testing.T) {
	assert := assert.New(t)
	fakeLogger := logTracker{}
	ch := ConversionHandler{errorLogger: &fakeLogger}
	fakeRequest := httptest.NewRequest(http.MethodPut, "http://device/config?", nil)

	t.Run("AddFlavorFormatErr", func(testing *testing.T) {
		ch.AddFlavorFormat = func(_ io.Reader, _ Vars, _ string, _ BodyReader) ([]byte, error) {
			return nil, errors.New(errMsg)
		}

		ch.ConversionADDHandler(nil, fakeRequest)

		assert.EqualValues(2, len(fakeLogger.vals))
		assert.EqualValues(2, len(fakeLogger.keys))
		assert.EqualValues(logging.ErrorKey(), fakeLogger.keys[1])
		assert.EqualValues(errMsg, fakeLogger.vals[1])
	})

	t.Run("AddFlavorNoError", func(testing *testing.T) {
		ch.AddFlavorFormat = func(_ io.Reader, _ Vars, _ string, _ BodyReader) ([]byte, error) {
			return expectedPayload, nil
		}

		var actualPayload []byte

		ch.SendData = func(_ time.Duration, _ http.ResponseWriter, response *wrp.SimpleRequestResponse) {
			actualPayload = response.Payload
		}

		ch.ConversionADDHandler(nil, fakeRequest)
		assert.EqualValues(expectedPayload, actualPayload)
	})
}
