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
	sampleNames      = []string{"p1", "p2"}
	dataType    int8 = 3
	value            = "someVal"
	name             = "someName"
	valid            = SetParam{Name: &name, Attributes: Attr{"notify": 0}}
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
		assert.True(strings.HasSuffix(err.Error(), "is required to be valid"))
	})
}

func TestSetFlavorFormat(t *testing.T) {
	c := ConversionWDMP{WRPSource: "dns:machineDNS"}
	commonURL := "http://device/config?k=v"
	var req *http.Request

	t.Run("DecodeErr", func(t *testing.T) {
		assert := assert.New(t)
		invalidBody := bytes.NewBufferString("{")
		req = httptest.NewRequest(http.MethodPatch, commonURL, invalidBody)
		_, err := c.SetFlavorFormat(req)
		assert.NotNil(err)
	})

	t.Run("InvalidData", func(t *testing.T) {
		assert := assert.New(t)
		emptyBody := bytes.NewBufferString(`{}`)
		req = httptest.NewRequest(http.MethodPatch, commonURL, emptyBody)

		_, err := c.SetFlavorFormat(req)
		assert.NotNil(err)
		assert.EqualValues(errInvalidSetWDMP, err)
	})

	t.Run("IdealSetAttrs", func(t *testing.T) {
		assert := assert.New(t)
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
		assert := assert.New(t)
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
		assert := assert.New(t)
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

	//Allow testSet with empty body and optional cmc header
	t.Run("EmptyBodyTestSet", func(t *testing.T) {
		assert := assert.New(t)

		req := httptest.NewRequest(http.MethodPatch, "http://device/config?k=v", nil)
		req.Header.Set(HeaderWPASyncNewCID, "newCid")
		req.Header.Set(HeaderWPASyncOldCID, "oldCid")

		wdmp, err := c.SetFlavorFormat(req)

		assert.Nil(err)
		assert.EqualValues(CommandTestSet, wdmp.Command)
		assert.Empty(wdmp.Parameters)
		assert.Empty(wdmp.SyncCmc)
		assert.EqualValues("oldCid", wdmp.OldCid)
		assert.EqualValues("newCid", wdmp.NewCid)
	})
}

func TestGetCommandForParam(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		assert := assert.New(t)
		assert.EqualValues(CommandSet, getCommandForParam(nil))
		assert.EqualValues(CommandSet, getCommandForParam([]SetParam{}))
	})

	//Attributes and Name are required properties for SET_ATTRS
	t.Run("SetCommandUndefinedAttributes", func(t *testing.T) {
		assert := assert.New(t)
		name := "setParam"
		setCommandParam := SetParam{Name: &name}
		assert.EqualValues(CommandSet, getCommandForParam([]SetParam{setCommandParam}))
	})

	//DataType and Value must be null for SET_ATTRS
	t.Run("SetAttrsCommand", func(t *testing.T) {
		assert := assert.New(t)
		name := "setAttrsParam"
		setCommandParam := SetParam{
			Name:       &name,
			Attributes: Attr{"zero": 0},
		}
		assert.EqualValues(CommandSetAttrs, getCommandForParam([]SetParam{setCommandParam}))
	})
}

func TestValidateAndDeduceSETCommand(t *testing.T) {
	assert := assert.New(t)
	c := ConversionWDMP{}

	t.Run("newCIDMissing", func(t *testing.T) {
		wdmp := new(SetWDMP)
		err := c.ValidateAndDeduceSET(http.Header{HeaderWPASyncOldCID: []string{"oldCID"}}, wdmp)
		assert.EqualValues(errNewCIDRequired, err)
	})

	t.Run("NilParams", func(t *testing.T) {
		wdmp := new(SetWDMP)
		err := c.ValidateAndDeduceSET(http.Header{}, wdmp)
		assert.EqualValues(errInvalidSetWDMP, err)
	})

	t.Run("TestSetNilValues", func(t *testing.T) {
		wdmp := new(SetWDMP)
		requestHeaders := http.Header{}
		requestHeaders.Add(HeaderWPASyncOldCID, "oldVal")
		requestHeaders.Add(HeaderWPASyncNewCID, "newVal")

		err := c.ValidateAndDeduceSET(requestHeaders, wdmp)
		assert.Nil(err)
		assert.EqualValues(CommandTestSet, wdmp.Command)
	})
}

func TestIsValidSetWDMP(t *testing.T) {
	t.Run("TestAndSetZeroParams", func(t *testing.T) {
		assert := assert.New(t)

		wdmp := &SetWDMP{Command: CommandTestSet} //nil parameters
		assert.True(isValidSetWDMP(wdmp))

		wdmp = &SetWDMP{Command: CommandTestSet, Parameters: []SetParam{}} //empty parameters
		assert.True(isValidSetWDMP(wdmp))
	})

	t.Run("NilNameInParam", func(t *testing.T) {
		assert := assert.New(t)

		dataType := int8(0)
		nilNameParam := SetParam{
			Value:    "val",
			DataType: &dataType,
			// Name is left undefined
		}
		params := []SetParam{nilNameParam}
		wdmp := &SetWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("NilDataTypeNonNilValue", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := SetParam{
			Name:  &name,
			Value: 3,
			//DataType is left undefined
		}
		params := []SetParam{param}
		wdmp := &SetWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("SetAttrsParamNilAttr", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := SetParam{
			Name: &name,
		}
		params := []SetParam{param}
		wdmp := &SetWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("MixedParams", func(t *testing.T) {
		assert := assert.New(t)

		name, dataType := "victorious", int8(1)
		setAttrParam := SetParam{
			Name:       &name,
			Attributes: map[string]interface{}{"three": 3},
		}

		setParam := SetParam{
			Name:       &name,
			Attributes: map[string]interface{}{"two": 2},
			Value:      3,
			DataType:   &dataType,
		}
		mixParams := []SetParam{setAttrParam, setParam}
		wdmp := &SetWDMP{Command: CommandSetAttrs, Parameters: mixParams}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("IdealSet", func(t *testing.T) {
		assert := assert.New(t)

		name := "victorious"
		setAttrParam := SetParam{
			Name:       &name,
			Attributes: map[string]interface{}{"three": 3},
		}
		params := []SetParam{setAttrParam}
		wdmp := &SetWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.True(isValidSetWDMP(wdmp))
	})
}
func TestDeleteFlavorFormat(t *testing.T) {
	assert := assert.New(t)
	commonVars := Vars{"param": "rowName", "emptyParam": ""}
	c := ConversionWDMP{WRPSource: "dns:machineDNS"}

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
	commonVars := Vars{"uThere?": "yes!"}
	emptyVars := Vars{}
	c := ConversionWDMP{WRPSource: "dns:machineDNS"}

	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)
		_, err := c.ReplaceFlavorFormat(nil, emptyVars, "uThere?")
		assert.NotNil(err)
		assert.EqualValues(errTableNameRequired, err)
	})

	t.Run("DecodeJSONErr", func(t *testing.T) {
		assert := assert.New(t)
		var buffer bytes.Buffer
		buffer.WriteString("{")
		_, err := c.ReplaceFlavorFormat(&buffer, commonVars, "uThere?")
		assert.NotNil(err)
		assert.Contains(err.Error(), "JSON")
	})

	t.Run("BlankDataAllowed", func(t *testing.T) {
		assert := assert.New(t)
		var buffer bytes.Buffer
		buffer.WriteString("{}")
		_, err := c.ReplaceFlavorFormat(&buffer, commonVars, "uThere?")
		assert.Nil(err)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)
		var buffer bytes.Buffer
		buffer.WriteString(`{"0":{"uno":"one","dos":"two"}}`)

		wdmp, err := c.ReplaceFlavorFormat(&buffer, commonVars, "uThere?")

		assert.Nil(err)
		assert.EqualValues(wdmpReplace, wdmp)
	})
}

func TestAddFlavorFormat(t *testing.T) {
	emptyVars := Vars{}

	c := ConversionWDMP{WRPSource: "dns:machineDNS"}

	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		_, err := c.AddFlavorFormat(nil, emptyVars, "uThere?")
		assert.EqualValues(errTableNameRequired, err)
	})

	t.Run("DecodeJSONErr", func(t *testing.T) {
		assert := assert.New(t)

		var buffer bytes.Buffer
		buffer.WriteString("{")

		_, err := c.AddFlavorFormat(&buffer, commonVars, "uThere?")
		assert.Contains(err.Error(), "JSON")
	})

	t.Run("EmptyData", func(t *testing.T) {
		assert := assert.New(t)

		var buffer bytes.Buffer
		buffer.WriteString("{}")

		_, err := c.AddFlavorFormat(&buffer, commonVars, "uThere?")

		assert.Nil(err)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)

		var buffer bytes.Buffer
		buffer.WriteString(`{"uno":"one","dos":"two"}`)

		wdmp, err := c.AddFlavorFormat(&buffer, commonVars, "uThere?")

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

func TestGetConfiguredWRP(t *testing.T) {
	assert := assert.New(t)
	deviceID := "mac:112233445566"
	service := "webpaService"
	tid := "uniqueVal"

	c := ConversionWDMP{WRPSource: "dns:source"}

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
