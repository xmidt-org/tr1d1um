package main

import (
	"testing"
	"encoding/json"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
	"net/http"
)

//Some tests are trivial but worth having
func TestWrapInWrp(t *testing.T) {
	assert := assert.New(t)
	input := []byte("data")
	wrpMsg := wrp.Message{Type:wrp.SimpleRequestResponseMessageType, Payload:input}
	expected, expectedErr := json.Marshal(wrpMsg)

	actual, actualErr := WrapInWrp(input)
	assert.EqualValues(expected, actual)
	assert.EqualValues(expectedErr, actualErr)
}

func TestGetFormattedData(t *testing.T) {
	assert := assert.New(t)
	req, err := http.NewRequest("GET", "api/device/config?names=param1,param2,param3", nil)

	if err != nil {
		assert.FailNow("Could not make new request")
	}

	wdmp := &WDMP{Command:"GET"}
	wdmp.Names = []string{"param1","param2","param3"}

	expected, expectedErr := json.Marshal(wdmp)

	actual, actualErr := GetFormattedData(req,"names", ",")

	assert.EqualValues(expected, actual)
	assert.EqualValues(expectedErr, actualErr)

}
