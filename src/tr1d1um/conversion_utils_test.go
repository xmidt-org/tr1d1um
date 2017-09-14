package main

import (
	"testing"
	"encoding/json"
	"github.com/stretchr/testify/assert"
	"net/http"
)

func TestGetFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	req, err := http.NewRequest("GET", "api/device/config?names=param1,param2,param3", nil)

	if err != nil {
		assert.FailNow("Could not make new request")
	}

	wdmp := &GetWDMP{Command:COMMAND_GET}
	wdmp.Names = []string{"param1","param2","param3"}

	expected, expectedErr := json.Marshal(wdmp)

	actual, actualErr := GetFlavorFormat(req,"attributes", "names", ",")

	assert.EqualValues(expected, actual)
	assert.EqualValues(expectedErr, actualErr)

}


