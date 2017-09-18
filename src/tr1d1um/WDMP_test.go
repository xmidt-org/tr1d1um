package main

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

/* tests correct usage of library */

func TestValidateSET(t *testing.T) {
	assert := assert.New(t)
	sp := SetParam{Attributes: Attr{"0": 0}, Name: &name, Value: value, DataType: &dataType}

	t.Run("Perfect", func(t *testing.T) {
		assert.Nil(sp.Validate())
	})

	t.Run("DataTypeIssue", func(t *testing.T) {
		sp.DataType = nil
		dataTypeErr := sp.Validate()
		assert.Contains(dataTypeErr, "dataType")
		sp.DataType = &dataType // put back value
	})

	t.Run("ValueIssue", func(t *testing.T) {
		sp.Value = nil
		valueErr := sp.Validate()
		assert.Contains(valueErr, "value")
		sp.Value = value
	})

	t.Run("Name", func(t *testing.T) {
		sp.Name = nil
		nameErr := sp.Validate()
		assert.Contains(nameErr, "name")
	})
}

func TestValidateSETAttrParams(t *testing.T) {
	assert := assert.New(t)

	t.Run("ZeroCases", func(t *testing.T) {
		errNil := ValidateSETAttrParams(nil)
		errEmpty := ValidateSETAttrParams([]SetParam{})

		assert.NotNil(errNil)
		assert.NotNil(errEmpty)
		assert.Contains(errNil.Error(), "invalid list")
		assert.Contains(errEmpty.Error(), "invalid list")
	})

	t.Run("InvalidAttr", func(t *testing.T) {
		param := SetParam{Attributes: Attr{}}
		err := ValidateSETAttrParams([]SetParam{param})
		assert.NotNil(err)
		assert.Contains(err.Error(), "invalid attr")
	})

	t.Run("Ideal", func(t *testing.T) {
		param := SetParam{Attributes: Attr{"notify": 0}}
		err := ValidateSETAttrParams([]SetParam{param})
		assert.Nil(err)
	})
}
