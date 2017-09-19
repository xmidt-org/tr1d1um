package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/stretchr/testify/assert"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	sampleNames        = []string{"p1", "p2"}
	dataType    int8   = 3
	value       string = "someVal"
	name        string = "someName"
)

func TestGetFlavorFormat(t *testing.T) {
	assert := assert.New(t)

	t.Run("IdealGet", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2", nil)

		wdmp := &GetWDMP{Command: COMMAND_GET, Names: sampleNames}

		expected, expectedErr := json.Marshal(wdmp)
		actual, actualErr := GetFlavorFormat(req, "attributes", "names", ",")

		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})

	t.Run("IdealGetAttr", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2&attributes=attr1",
			nil)

		wdmp := &GetWDMP{Command: COMMAND_GET_ATTRS, Names: sampleNames, Attribute: "attr1"}

		expected, expectedErr := json.Marshal(wdmp)
		actual, actualErr := GetFlavorFormat(req, "attributes", "names", ",")

		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})

	t.Run("NoNames", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=",
			nil)

		_, err := GetFlavorFormat(req, "attributes", "names", ",")

		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "names is a required"))
	})
}

func TestSetFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	cReader := CustomResultReader{}

	valid := SetParam{Name: &name, DataType: &dataType, Value: value, Attributes: Attr{"notify": 0}}

	req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", nil)

	t.Run("DecodeJsonErr", func(t *testing.T) {
		cReader.err = errors.New("JSON")
		_, err := SetFlavorFormat(req, cReader.CustomReader)
		assert.NotNil(err)
		assert.EqualValues("JSON", err.Error())
	})

	t.Run("InvalidData", func(t *testing.T) {
		cReader.data, cReader.err = []byte("{}"), nil
		_, err := SetFlavorFormat(req, cReader.CustomReader)
		assert.NotNil(err)
		assert.EqualValues("cannot be blank", err.Error())
	})

	t.Run("IdealPath", func(t *testing.T) {
		wdmpObj := &SetWDMP{Command: COMMAND_SET, Parameters: []SetParam{valid}}

		cReader.data = []byte(`{"parameters":[{"name": "someName","value":"someVal","attributes": {"notify": 0},
		"dataType": 3}]}`)

		actual, actualErr := SetFlavorFormat(req, cReader.CustomReader)
		expected, expectedErr := json.Marshal(wdmpObj)

		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})
}

func TestDeleteFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	commonVars := Vars{"param": "rowName", "emptyParam": ""}

	t.Run("NoRowName", func(t *testing.T) {
		_, err := DeleteFlavorFormat(Vars{}, "param")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "name is required"))
	})

	t.Run("EmptyRowName", func(t *testing.T) {
		_, err := DeleteFlavorFormat(commonVars, "emptyParam")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "name is required"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		wdmpObj := &DeleteRowWDMP{Command: COMMAND_DELETE_ROW, Row: "rowName"}
		expected, expectedErr := json.Marshal(wdmpObj)
		actual, actualErr := DeleteFlavorFormat(commonVars, "param")
		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})
}

func TestReplaceFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	cReader := CustomResultReader{}
	commonVars := Vars{"uThere?": "yes!"}
	emptyVars := Vars{}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := ReplaceFlavorFormat(nil, emptyVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJsonErr", func(t *testing.T) {
		cReader.data = []byte("")
		_, err := ReplaceFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "JSON"))
	})

	t.Run("InvalidParams", func(t *testing.T) {
		cReader.data = []byte("{}")
		_, err := ReplaceFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "blank"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		wdmpObj := &ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS, Table: commonVars["uThere?"]}
		wdmpObj.Rows = map[string]map[string]string{"0": {"uno": "one", "dos": "two"}}
		cReader.data = []byte(`{"0":{"uno":"one","dos":"two"}}`)

		actual, actualErr := ReplaceFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)

		expected, expectedErr := json.Marshal(wdmpObj)
		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})
}

func TestAddFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	cReader := CustomResultReader{}
	commonVars := Vars{"uThere?": "yes!"}
	emptyVars := Vars{}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := AddFlavorFormat(nil, emptyVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJsonErr", func(t *testing.T) {
		cReader.data = []byte("")
		_, err := AddFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "JSON"))
	})

	t.Run("EmptyData", func(t *testing.T) {
		cReader.data = []byte("{}")
		_, err := AddFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "data is empty"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		wdmpObj := &AddRowWDMP{Command: COMMAND_ADD_ROW, Table: commonVars["uThere?"]}
		wdmpObj.Row = map[string]string{"uno": "one", "dos": "two"}

		cReader.data = []byte(`{"uno":"one","dos":"two"}`)

		actual, actualErr := AddFlavorFormat(nil, commonVars, "uThere?", cReader.CustomReader)
		expected, expectedErr := json.Marshal(wdmpObj)

		assert.EqualValues(expected, actual)
		assert.EqualValues(expectedErr, actualErr)
	})
}

func TestGetFromUrlPath(t *testing.T) {
	assert := assert.New(t)

	fakeUrlVar := map[string]string{"k1": "k1v1,k1v2", "k2": "k2v1"}

	t.Run("NormalCases", func(t *testing.T) {

		k1ValGroup, exists := GetFromUrlPath("k1", fakeUrlVar)
		assert.True(exists)
		assert.EqualValues("k1v1,k1v2", k1ValGroup)

		k2ValGroup, exists := GetFromUrlPath("k2", fakeUrlVar)
		assert.True(exists)
		assert.EqualValues("k2v1", k2ValGroup)
	})

	t.Run("NonNilNonExistent", func(t *testing.T) {
		_, exists := GetFromUrlPath("k3", fakeUrlVar)
		assert.False(exists)
	})

	t.Run("NilCase", func(t *testing.T) {
		_, exists := GetFromUrlPath("k", nil)
		assert.False(exists)
	})
}

func TestDecodeJsonPayload(t *testing.T) {
	assert := assert.New(t)

	cReader := CustomResultReader{}

	t.Run("ReadEntireBodyFails", func(t *testing.T) {
		cReader.err = errors.New("fail")
		err := DecodeJsonPayload(nil, nil, cReader.CustomReader)

		assert.NotNil(err)
		assert.EqualValues(err.Error(), "fail")
	})

	t.Run("EmptyInput", func(t *testing.T) {
		emptyBody := bytes.NewBufferString("")
		cReader.data, cReader.err = []byte(""), nil

		err := DecodeJsonPayload(emptyBody, nil, cReader.CustomReader)

		assert.NotNil(err)
		assert.EqualValues(err, ErrJsonEmpty)
	})

	t.Run("IdealPath", func(t *testing.T) {
		cReader.data = []byte(`{"0":"zero","1":"one"}`)

		expected := map[string]string{"0": "zero", "1": "one"}
		actual := make(map[string]string)

		err := DecodeJsonPayload(nil, &actual, cReader.CustomReader)

		assert.Nil(err)
		assert.EqualValues(expected, actual)
	})
}

func TestValidateAndDeduceSETCommand(t *testing.T) {
	assert := assert.New(t)

	empty := []SetParam{}
	attrs := Attr{"attr1": 1, "attr2": "two"}

	noDataType := SetParam{Value: value, Name: &name}
	valid := SetParam{Name: &name, DataType: &dataType, Value: value}
	attrParam := SetParam{Name: &name, DataType: &dataType, Attributes: attrs}

	testAndSetHeader := http.Header{HEADER_WPA_SYNC_NEW_CID: []string{"newCid"}}
	emptyHeader := http.Header{}

	wdmp := new(SetWDMP)

	/* Tests with different possible failures */
	t.Run("NilParams", func(t *testing.T) {
		err := ValidateAndDeduceSET(http.Header{}, wdmp)
		assert.NotNil(err)
		assert.True(strings.Contains(err.Error(), "cannot be blank"))
		assert.EqualValues("", wdmp.Command)
	})

	t.Run("EmptyParams", func(t *testing.T) {
		wdmp.Parameters = empty
		err := ValidateAndDeduceSET(http.Header{}, wdmp)
		assert.NotNil(err)
		assert.True(strings.Contains(err.Error(), "cannot be blank"))
		assert.EqualValues("", wdmp.Command)
	})

	//Will attempt at validating SET_ATTR properties instead
	t.Run("MissingSETProperty", func(t *testing.T) {
		wdmp.Parameters = append(empty, noDataType)
		err := ValidateAndDeduceSET(emptyHeader, wdmp)
		assert.EqualValues("invalid attr", err.Error())
		assert.EqualValues("", wdmp.Command)
	})

	/* Ideal command cases */
	t.Run("MultipleValidSET", func(t *testing.T) {
		wdmp.Parameters = append(empty, valid, valid)
		assert.Nil(ValidateAndDeduceSET(emptyHeader, wdmp))
		assert.EqualValues(COMMAND_SET, wdmp.Command)
	})

	t.Run("MultipleValidTEST_SET", func(t *testing.T) {
		wdmp.Parameters = append(empty, valid, valid)
		assert.Nil(ValidateAndDeduceSET(testAndSetHeader, wdmp))
		assert.EqualValues(COMMAND_TEST_SET, wdmp.Command)
	})

	t.Run("MultipleValidSET_ATTRS", func(t *testing.T) {
		wdmp.Parameters = append(empty, attrParam, attrParam)
		assert.Nil(ValidateAndDeduceSET(emptyHeader, wdmp))
		assert.EqualValues(COMMAND_SET_ATTRS, wdmp.Command)
	})
}

func TestSetOrLeave(t *testing.T) {
	assert := assert.New(t)
	assert.EqualValues("fallback", SetOrLeave("fallback", ""))
	assert.EqualValues("", SetOrLeave("", ""))
	assert.EqualValues("theNewVal", SetOrLeave("", "theNewVal"))
}

func TestExtractPayloadFromWrp(t *testing.T) {
	assert := assert.New(t)
	cr := CustomResultReader{}
	cr.err = errors.New("error reading")

	t.Run("SomeErrorOccurred", func(t *testing.T) {
		payload, err := ExtractPayloadFromWrp(nil, cr.CustomReader)
		assert.Nil(payload)
		assert.NotNil(err)
		assert.EqualValues(cr.err, err)
	})

	t.Run("IdealCase", func(t *testing.T) {
		//cr.err = nil
		//cr.data = []byte(`{"msg_type": 3, "payload": j}`) //todo: Need to show correct payload extraction
		//expected := []byte("b")
		//
		//payload, err := ExtractPayloadFromWrp(nil, cr.CustomReader)
		//assert.NotNil(payload)
		//assert.Nil(err)
		//assert.EqualValues(expected, payload)
	})
}

/*Set the data and err fields and the next call to CustomReader will return them*/
type CustomResultReader struct {
	data []byte
	err  error
}

func (c CustomResultReader) CustomReader(_ io.Reader) ([]byte, error) {
	return c.data, c.err
}
