package main

import (
	"bytes"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

var (
	sampleNames             = []string{"p1", "p2"}
	dataType         int8   = 3
	value            string = "someVal"
	name             string = "someName"
	valid                   = SetParam{Name: &name, DataType: &dataType, Value: value, Attributes: Attr{"notify": 0}}
	emptyInputBuffer bytes.Buffer
	commonVars       = Vars{"uThere?": "yes!"}
	replaceRows      = IndexRow{"0": {"uno": "one", "dos": "two"}}
	addRows          = map[string]string{"uno": "one", "dos": "two"}

	wdmpGet      = &GetWDMP{Command: COMMAND_GET, Names: sampleNames}
	wdmpGetAttrs = &GetWDMP{Command: COMMAND_GET_ATTRS, Names: sampleNames, Attribute: "attr1"}
	wdmpSet      = &SetWDMP{Command: COMMAND_SET, Parameters: []SetParam{valid}}
	wdmpDel      = &DeleteRowWDMP{Command: COMMAND_DELETE_ROW, Row: "rowName"}
	wdmpReplace  = &ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS, Table: commonVars["uThere?"], Rows: replaceRows}
	wdmpAdd      = &AddRowWDMP{Command: COMMAND_ADD_ROW, Table: commonVars["uThere?"], Row: addRows}
)

func TestGetFlavorFormat(t *testing.T) {
	assert := assert.New(t)

	t.Run("IdealGet", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2", nil)

		wdmp, err := GetFlavorFormat(req, "attributes", "names", ",")

		assert.Nil(err)
		assert.EqualValues(wdmpGet, wdmp)
	})

	t.Run("IdealGetAttr", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2&attributes=attr1",
			nil)

		wdmp, err := GetFlavorFormat(req, "attributes", "names", ",")

		assert.Nil(err)
		assert.EqualValues(wdmpGetAttrs, wdmp)
	})

	t.Run("NoNames", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=",
			nil)

		_, err := GetFlavorFormat(req, "attributes", "names", ",")

		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "names is a required"))
	})
}

/*
func TestSetFlavorFormat(t *testing.T) {
	assert := assert.New(t)


	t.Run("DecodeErr", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", bytes.NewBufferString(`{`))
		_, err := SetFlavorFormat(req)
		assert.NotNil(err)
	})

	t.Run("InvalidData", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", bytes.NewBufferString(`{}`))
		_, err := SetFlavorFormat(req)
		assert.NotNil(err)
		assert.EqualValues("cannot be blank", err.Error())
	})

	t.Run("IdealSet", func(t *testing.T) {
		body := bytes.NewBufferString(`{"parameters":[{"name":"someName","attributes":{"notify": 0}}]}`)

		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", body)

		wdmp, err := SetFlavorFormat(req)

		assert.Nil(err)
		assert.EqualValues(wdmpSet, wdmp)
	})
}
*/

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
		wdmp, err := DeleteFlavorFormat(commonVars, "param")
		assert.Nil(err)
		assert.EqualValues(wdmpDel, wdmp)
	})
}

/*
func TestReplaceFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	commonVars := Vars{"uThere?": "yes!"}
	emptyVars := Vars{}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := ReplaceFlavorFormat(nil, emptyVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJsonErr", func(t *testing.T) {
		_, err := ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "JSON"))
	})

	t.Run("InvalidParams", func(t *testing.T) {
		_, err := ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "blank"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		emptyInputBuffer.WriteString(`{"0":{"uno":"one","dos":"two"}}`)

		wdmp, err := ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")

		assert.Nil(err)
		assert.EqualValues(wdmpReplace, wdmp)

		emptyInputBuffer.Reset()
	})
}

func TestAddFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	emptyVars := Vars{}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := AddFlavorFormat(nil, emptyVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJsonErr", func(t *testing.T) {
		_, err := AddFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "JSON"))
	})

	t.Run("EmptyData", func(t *testing.T) {
		emptyInputBuffer.WriteString("{}")
		_, err := AddFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "data is empty"))

		emptyInputBuffer.Reset()
	})

	t.Run("IdealPath", func(t *testing.T) {
		emptyInputBuffer.WriteString(`{"uno":"one","dos":"two"}`)

		wdmp, err := AddFlavorFormat(nil, commonVars, "uThere?")

		assert.Nil(err)
		assert.EqualValues(wdmpAdd, wdmp)

		emptyInputBuffer.Reset()
	})
}

*/

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

/*
func TestExtractPayloadFromWrp(t *testing.T) {
	assert := assert.New(t)
	cr := CustomResultReader{}
	cr.err = errors.New("error reading")

	t.Run("SomeErrorOccurred", func(t *testing.T) {
		payload, err := ExtractPayload(nil, cr.CustomReader)
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
