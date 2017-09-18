package main

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

/*
func TestConversionGETHandlerWrapFailure(t *testing.T) {
	assert := assert.New(t)
	conversionHanlder := new(ConversionHandler)
	SetupTestingConditions(true, false, conversionHanlder)
	req, err := http.NewRequest("GET", "/device/config?names=param1,param2", nil)
	if err != nil {
		assert.FailNow("Could not make new request")
	}
	conversionHanlder.ConversionGETHandler(nil, req)
	errorMessage := conversionHanlder.errorLogger.(*logTracker).vals[0].(string)
	assert.EqualValues(ERR_UNSUCCESSFUL_DATA_WRAP,errorMessage)
}
*/
//todo: more cases
