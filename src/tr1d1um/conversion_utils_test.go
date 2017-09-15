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

var sampleNames = []string{"p1", "p2"}

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

func TestAddFlavorFormat(t *testing.T) {

}

func TestDeleteFlavorFormat(t *testing.T) {

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
		assert.True(strings.HasPrefix(err.Error(), "invalid"))
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

type CustomResultReader struct {
	data []byte
	err  error
}

func (c CustomResultReader) CustomReader(_ io.Reader) ([]byte, error) {
	return c.data, c.err
}
