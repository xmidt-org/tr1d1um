package main

import (
	"testing"
	"errors"
	"net/http"
	"github.com/stretchr/testify/assert"
	"strings"
)

type logTracker struct {
	keys []interface{}
	vals []interface{}
}

func (fake *logTracker) Log(keyvals ...interface{}) (err error) {
	for i, keyval := range keyvals{
		if i % 2 == 0{
			fake.keys = append(fake.keys, keyval)
		} else {
			fake.vals = append(fake.vals, keyval)
		}
	}
	return
}

func TestConversionGETHandlerWrapFailure(t *testing.T) {
	assert := assert.New(t)
	conversionHanlder := new(ConversionHandler)
	SetupTestingConditions(true, false, conversionHanlder)
	req, err := http.NewRequest("GET", "/device/config?names=param1;param2", nil)
	if err != nil {
		assert.FailNow("Could not make new request")
	}
	conversionHanlder.ConversionGETHandler(nil, req)
	errorMessage := conversionHanlder.errorLogger.(*logTracker).vals[0].(string)
	assert.True(strings.HasPrefix(errorMessage, "Could not wrap wdmp"))
}

//todo: more cases


func SetupTestingConditions(failWrap, failFormat bool, conversionHandler *ConversionHandler) {
	logger := logTracker{}
	conversionHandler.errorLogger = &logger
	conversionHandler.WrapInWrp = func(bytes []byte) (data []byte, err error) {
		if failWrap {
			err = errors.New("wrapinwrp: always failing")
		}
		return
	}

	conversionHandler.GetFormattedData = func(request *http.Request, i string, i2 string) (data []byte, err error) {
		if failFormat {
			err = errors.New("getformatteddata: always failing")
		}
		return
	}
}
