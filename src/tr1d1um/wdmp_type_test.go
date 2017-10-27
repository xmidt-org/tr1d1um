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
	"testing"

	"github.com/stretchr/testify/assert"
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
