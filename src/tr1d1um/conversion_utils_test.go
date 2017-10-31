/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
)

var (
	sampleNames           = []string{"p1", "p2"}
	dataType         int8 = 3
	value                 = "someVal"
	name                  = "someName"
	valid                 = SetParam{Name: &name, Attributes: Attr{"notify": 0}}
	emptyInputBuffer bytes.Buffer
	commonVars       = Vars{"uThere?": "yes!"}
	replaceRows      = IndexRow{"0": {"uno": "one", "dos": "two"}}
	addRows          = map[string]string{"uno": "one", "dos": "two"}

	wdmpGet      = &GetWDMP{Command: CommandGet, Names: sampleNames}
	wdmpGetAttrs = &GetWDMP{Command: CommandGetAttrs, Names: sampleNames, Attribute: "attr1"}
	wdmpSet      = &SetWDMP{Command: CommandSetAttrs, Parameters: []SetParam{valid}}
	wdmpDel      = &DeleteRowWDMP{Command: CommandDeleteRow, Row: "rowName"}
	wdmpReplace  = &ReplaceRowsWDMP{Command: CommandReplaceRows, Table: commonVars["uThere?"], Rows: replaceRows}
	wdmpAdd      = &AddRowWDMP{Command: CommandAddRow, Table: commonVars["uThere?"], Row: addRows}
)

func TestGetFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	c := ConversionWDMP{}

	t.Run("IdealGet", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2", nil)

		wdmp, err := c.GetFlavorFormat(req, nil, "attributes", "names", ",")

		assert.Nil(err)
		assert.EqualValues(wdmpGet, wdmp)
	})

	t.Run("IdealGetStat", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/mac:112233445566/stat", nil)

		wdmp, err := c.GetFlavorFormat(req, map[string]string{"service": "stat"}, "attributes", "names", ",")

		assert.Nil(err)
		assert.EqualValues(new(GetWDMP), wdmp)
	})

	t.Run("IdealGetAttr", func(t *testing.T) {

		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=p1,p2&attributes=attr1",
			nil)

		wdmp, err := c.GetFlavorFormat(req, nil, "attributes", "names", ",")

		assert.Nil(err)
		assert.EqualValues(wdmpGetAttrs, wdmp)
	})

	t.Run("NoNames", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "http://api/device/config?names=",
			nil)

		_, err := c.GetFlavorFormat(req, nil, "attributes", "names", ",")

		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "names is a required"))
	})
}

func TestSetFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	c := ConversionWDMP{encodingHelper:&EncodingHelper{}, WRPSource: "dns:machineDNS"}
	commonURL := "http://device/config?k=v"
	var req *http.Request

	t.Run("DecodeErr", func(t *testing.T) {
		invalidBody := bytes.NewBufferString("{")
		req = httptest.NewRequest(http.MethodPatch, commonURL, invalidBody)
		_, err := c.SetFlavorFormat(req)
		assert.NotNil(err)
	})

	t.Run("InvalidData", func(t *testing.T) {
		emptyBody := bytes.NewBufferString(`{}`)
		req = httptest.NewRequest(http.MethodPatch, commonURL, emptyBody)

		_, err := c.SetFlavorFormat(req)
		assert.NotNil(err)
		assert.EqualValues("cannot be blank", err.Error())
	})

	t.Run("IdealSetAttrs", func(t *testing.T) {
		input := bytes.NewBufferString(`{"parameters":[{"name": "someName","attributes":
		{"notify": 0}}]}`)

		req = httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", input)

		wdmp, err := c.SetFlavorFormat(req)

		assert.Nil(err)
		assert.EqualValues(wdmpSet.Command, wdmp.Command)
		assert.EqualValues(name, *wdmp.Parameters[0].Name)
		assert.EqualValues(valid.Attributes["notify"], wdmp.Parameters[0].Attributes["notify"])
	})

	t.Run("IdealSet", func(t *testing.T) {
		input := bytes.NewBufferString(`{"parameters":[{"name": "someName","value":"someVal","dataType":3}]}`)

		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", input)

		wdmp, err := c.SetFlavorFormat(req)

		assert.Nil(err)
		assert.EqualValues(CommandSet, wdmp.Command)
		assert.EqualValues(name, *wdmp.Parameters[0].Name)
		assert.EqualValues(value, wdmp.Parameters[0].Value)
		assert.EqualValues(3, *wdmp.Parameters[0].DataType)
	})

	t.Run("IdealTestSet", func(t *testing.T) {
		input := bytes.NewBufferString(`{"parameters":[{"name": "someName","value":"someVal","dataType":3}]}`)

		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", input)
		req.Header.Set(HeaderWPASyncCMC, "sync-val")
		req.Header.Set(HeaderWPASyncNewCID, "newCid")

		wdmp, err := c.SetFlavorFormat(req)

		assert.Nil(err)
		assert.EqualValues(CommandTestSet, wdmp.Command)
		assert.EqualValues(name, *wdmp.Parameters[0].Name)
		assert.EqualValues(value, wdmp.Parameters[0].Value)
		assert.EqualValues(3, *wdmp.Parameters[0].DataType)
		assert.EqualValues("sync-val", wdmp.SyncCmc)
		assert.EqualValues("newCid", wdmp.NewCid)
	})
}

func TestDeleteFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	commonVars := Vars{"param": "rowName", "emptyParam": ""}
	c := ConversionWDMP{encodingHelper:&EncodingHelper{}, WRPSource: "dns:machineDNS"}

	t.Run("NoRowName", func(t *testing.T) {
		_, err := c.DeleteFlavorFormat(Vars{}, "param")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "name is required"))
	})

	t.Run("EmptyRowName", func(t *testing.T) {
		_, err := c.DeleteFlavorFormat(commonVars, "emptyParam")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "name is required"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		wdmp, err := c.DeleteFlavorFormat(commonVars, "param")
		assert.Nil(err)
		assert.EqualValues(wdmpDel, wdmp)
	})
}

func TestReplaceFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	commonVars := Vars{"uThere?": "yes!"}
	emptyVars := Vars{}
	c := ConversionWDMP{encodingHelper:&EncodingHelper{}, WRPSource: "dns:machineDNS"}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := c.ReplaceFlavorFormat(nil, emptyVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJSONErr", func(t *testing.T) {
		_, err := c.ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.Contains(err.Error(), "JSON")
	})

	t.Run("InvalidParams", func(t *testing.T) {
		defer emptyInputBuffer.Reset()
		emptyInputBuffer.WriteString("{}")
		_, err := c.ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "blank"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		emptyInputBuffer.WriteString(`{"0":{"uno":"one","dos":"two"}}`)

		wdmp, err := c.ReplaceFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")

		assert.Nil(err)
		assert.EqualValues(wdmpReplace, wdmp)

		emptyInputBuffer.Reset()
	})
}

func TestAddFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	emptyVars := Vars{}

	c := ConversionWDMP{encodingHelper:&EncodingHelper{}, WRPSource: "dns:machineDNS"}

	t.Run("TableNotProvided", func(t *testing.T) {
		_, err := c.AddFlavorFormat(nil, emptyVars, "uThere?")
		assert.NotNil(err)
		assert.True(strings.HasPrefix(err.Error(), "tableName"))
	})

	t.Run("DecodeJSONErr", func(t *testing.T) {
		_, err := c.AddFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.Contains(err.Error(), "JSON")
	})

	t.Run("EmptyData", func(t *testing.T) {
		defer emptyInputBuffer.Reset()

		emptyInputBuffer.WriteString("{}")

		_, err := c.AddFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")

		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "blank"))
	})

	t.Run("IdealPath", func(t *testing.T) {
		defer emptyInputBuffer.Reset()

		emptyInputBuffer.WriteString(`{"uno":"one","dos":"two"}`)

		wdmp, err := c.AddFlavorFormat(&emptyInputBuffer, commonVars, "uThere?")

		assert.Nil(err)
		assert.EqualValues(wdmpAdd, wdmp)
	})
}

func TestGetFromURLPath(t *testing.T) {
	assert := assert.New(t)

	fakeURLVar := map[string]string{"k1": "k1v1,k1v2", "k2": "k2v1"}
	c := ConversionWDMP{}

	t.Run("NormalCases", func(t *testing.T) {

		k1ValGroup, exists := c.GetFromURLPath("k1", fakeURLVar)
		assert.True(exists)
		assert.EqualValues("k1v1,k1v2", k1ValGroup)

		k2ValGroup, exists := c.GetFromURLPath("k2", fakeURLVar)
		assert.True(exists)
		assert.EqualValues("k2v1", k2ValGroup)
	})

	t.Run("NonNilNonExistent", func(t *testing.T) {
		_, exists := c.GetFromURLPath("k3", fakeURLVar)
		assert.False(exists)
	})

	t.Run("NilCase", func(t *testing.T) {
		_, exists := c.GetFromURLPath("k", nil)
		assert.False(exists)
	})
}

func TestValidateAndDeduceSETCommand(t *testing.T) {
	assert := assert.New(t)

	empty := []SetParam{}
	attrs := Attr{"attr1": 1, "attr2": "two"}

	c := ConversionWDMP{}
	noDataType := SetParam{Value: value, Name: &name}
	valid := SetParam{Name: &name, DataType: &dataType, Value: value}
	attrParam := SetParam{Name: &name, DataType: &dataType, Attributes: attrs}

	testAndSetHeader := http.Header{HeaderWPASyncNewCID: []string{"newCid"}}
	emptyHeader := http.Header{}

	wdmp := new(SetWDMP)

	// Tests with different possible failures
	t.Run("NilParams", func(t *testing.T) {
		err := c.ValidateAndDeduceSET(http.Header{}, wdmp)
		assert.NotNil(err)
		assert.True(strings.Contains(err.Error(), "cannot be blank"))
		assert.EqualValues("", wdmp.Command)
	})

	t.Run("EmptyParams", func(t *testing.T) {
		wdmp.Parameters = empty
		err := c.ValidateAndDeduceSET(http.Header{}, wdmp)
		assert.NotNil(err)
		assert.True(strings.Contains(err.Error(), "cannot be blank"))
		assert.EqualValues("", wdmp.Command)
	})

	//Will attempt at validating SET_ATTR properties instead
	t.Run("MissingSETProperty", func(t *testing.T) {
		wdmp.Parameters = append(empty, noDataType)
		err := c.ValidateAndDeduceSET(emptyHeader, wdmp)
		assert.EqualValues("invalid attr", err.Error())
		assert.EqualValues("", wdmp.Command)
	})

	// Ideal command cases
	t.Run("MultipleValidSET", func(t *testing.T) {
		wdmp.Parameters = append(empty, valid, valid)
		assert.Nil(c.ValidateAndDeduceSET(emptyHeader, wdmp))
		assert.EqualValues(CommandSet, wdmp.Command)
	})

	t.Run("MultipleValidTEST_SET", func(t *testing.T) {
		wdmp.Parameters = append(empty, valid, valid)
		assert.Nil(c.ValidateAndDeduceSET(testAndSetHeader, wdmp))
		assert.EqualValues(CommandTestSet, wdmp.Command)
	})

	t.Run("MultipleValidSET_ATTRS", func(t *testing.T) {
		wdmp.Parameters = append(empty, attrParam, attrParam)
		assert.Nil(c.ValidateAndDeduceSET(emptyHeader, wdmp))
		assert.EqualValues(CommandSetAttrs, wdmp.Command)
	})
}

func TestDecodeJSON(t *testing.T) {
	assert := assert.New(t)
	e := EncodingHelper{}

	t.Run("IdealPath", func(t *testing.T) {
		input := bytes.NewBufferString(`{"0":"zero","1":"one"}`)

		expected := map[string]string{"0": "zero", "1": "one"}
		actual := make(map[string]string)

		err := e.DecodeJSON(input, &actual)

		assert.Nil(err)
		assert.EqualValues(expected, actual)
	})

	t.Run("JsonErr", func(t *testing.T) {
		actual := make(map[string]string)

		err := e.DecodeJSON(bytes.NewBufferString("{"), &actual)
		assert.NotNil(err)
	})
}

func TestEncodeJSON(t *testing.T) {
	e := EncodingHelper{}
	assert := assert.New(t)
	expected := []byte(`{"command":"GET","names":["p1","p2"]}`)
	actual, err := e.EncodeJSON(wdmpGet)
	assert.Nil(err)
	assert.EqualValues(expected, actual)
}

func TestExtractPayloadFromWrp(t *testing.T) {
	assert := assert.New(t)
	e := EncodingHelper{}

	t.Run("IdealScenario", func(t *testing.T) {
		expectedPayload := []byte("expectMe")
		wrpMsg := wrp.Message{Payload: expectedPayload}
		var inputBuffer bytes.Buffer

		wrp.NewEncoder(&inputBuffer, wrp.JSON).Encode(wrpMsg)

		payload, err := e.ExtractPayload(&inputBuffer, wrp.JSON)

		assert.Nil(err)
		assert.EqualValues(expectedPayload, payload)
	})

	t.Run("DecodingErr", func(t *testing.T) {
		badInput := bytes.NewBufferString("{")

		_, err := e.ExtractPayload(badInput, wrp.JSON)

		assert.NotNil(err)
	})
}

/*
This test is testing code that's already tested in webpa-common.
there are too many layers of abstraction here.  Remove conversion_utils, and just use webpa-common

func TestGenericEncode(t *testing.T) {
	assert := assert.New(t)
	e := EncodingHelper{}
	wrpMsg := wrp.Message{Type: wrp.SimpleRequestResponseMessageType, Destination: "someDestination"}
	expectedEncoding := []byte(`{"msg_type":3,"dest":"someDestination"}`)

	actualEncoding, err := e.GenericEncode(&wrpMsg, wrp.JSON)

	assert.Nil(err)
	assert.EqualValues(expectedEncoding, actualEncoding)
}
*/

func TestGetConfiguredWRP(t *testing.T) {
	assert := assert.New(t)
	deviceID := "mac:112233445566"
	service := "webpaService"
	tid := "uniqueVal"

	c := ConversionWDMP{WRPSource:"dns:source"}

	inputVars := Vars{"service": service, "deviceid": deviceID}
	inputHeader := http.Header{}
	inputHeader.Set("Content-Type", wrp.JSON.ContentType())
	inputHeader.Set(HeaderWPATID, tid)
	inputWdmpPayload := []byte(`{irrelevantFormat}`)

	expectedSource := "dns:source/" + service
	expectedDest := deviceID + "/" + service

	wrpMsg := c.GetConfiguredWRP(inputWdmpPayload, inputVars, inputHeader)

	assert.NotNil(wrpMsg)
	assert.EqualValues(wrp.JSON.ContentType(), wrpMsg.ContentType)
	assert.EqualValues(inputWdmpPayload, wrpMsg.Payload)
	assert.EqualValues(wrp.SimpleRequestResponseMessageType, wrpMsg.Type)
	assert.EqualValues(expectedDest, wrpMsg.Destination)
	assert.EqualValues(expectedSource, wrpMsg.Source)
	assert.EqualValues(tid, wrpMsg.TransactionUUID)
}

func TestGetOrGenTID(t *testing.T) {
	assert := assert.New(t)
	t.Run("UseGivenTID", func(t *testing.T) {
		header := http.Header{}
		header.Set(HeaderWPATID, "SomeTID")
		assert.EqualValues("SomeTID", GetOrGenTID(header))
	})

	t.Run("GenerateTID", func(t *testing.T) {
		tid := GetOrGenTID(http.Header{})
		assert.NotEmpty(tid)
	})
}
