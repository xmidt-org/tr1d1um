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
	"errors"

	"github.com/go-ozzo/ozzo-validation"
)

//All the supported commands, WebPA Headers and misc
const (
	CommandGet         = "GET"
	CommandGetAttrs    = "GET_ATTRIBUTES"
	CommandSet         = "SET"
	CommandSetAttrs    = "SET_ATTRIBUTES"
	CommandTestSet     = "TEST_AND_SET"
	CommandAddRow      = "ADD_ROW"
	CommandDeleteRow   = "DELETE_ROW"
	CommandReplaceRows = "REPLACE_ROWS"

	HeaderWPASyncOldCID = "X-Webpa-Sync-Old-Cid"
	HeaderWPASyncNewCID = "X-Webpa-Sync-New-Cid"
	HeaderWPASyncCMC    = "X-Webpa-Sync-Cmc"
	HeaderWPATID        = "X-WebPA-Transaction-Id"

	ErrUnsuccessfulDataParse = "Unsuccessful Data Parse"
)

//GetWDMP serves as container for data used for the GET-flavored commands
type GetWDMP struct {
	Command   string   `json:"command"`
	Names     []string `json:"names,omitempty"`
	Attribute string   `json:"attributes,omitempty"`
}

//SetParam defines the structure for Parameters read from json data. Applicable to the SET-flavored commands
type SetParam struct {
	Name       *string     `json:"name"`
	DataType   *int8       `json:"dataType,omitempty"`
	Value      interface{} `json:"value,omitempty"`
	Attributes Attr        `json:"attributes,omitempty"`
}

//Attr facilitates reading in json data containing attributes applicable to the GET command
type Attr map[string]interface{}

//SetWDMP serves as container for data used for the SET-flavored commands
type SetWDMP struct {
	Command    string     `json:"command"`
	OldCid     string     `json:"old-cid,omitempty"`
	NewCid     string     `json:"new-cid,omitempty"`
	SyncCmc    string     `json:"sync-cmc,omitempty"`
	Parameters []SetParam `json:"parameters,omitempty"`
}

//AddRowWDMP serves as container for data used for the ADD_ROW command
type AddRowWDMP struct {
	Command string            `json:"command"`
	Table   string            `json:"table"`
	Row     map[string]string `json:"row"`
}

//ReplaceRowsWDMP serves as container for data used for the REPLACE_ROWS command
type ReplaceRowsWDMP struct {
	Command string   `json:"command"`
	Table   string   `json:"table"`
	Rows    IndexRow `json:"rows"`
}

//IndexRow facilitates data transfer from json data of the form {index1: {key:val}, index2: {key:val}, ... }
type IndexRow map[string]map[string]string

//DeleteRowWDMP contains data used in the DELETE_ROW command
type DeleteRowWDMP struct {
	Command string `json:"command"`
	Row     string `json:"row"`
}

//Validate defines the validation rules applicable to SetParam in the context of the SET and TEST_SET commands
func (sp SetParam) Validate() error {
	return validation.ValidateStruct(&sp,
		validation.Field(&sp.Name, validation.NotNil),
		validation.Field(&sp.DataType, validation.NotNil),
		validation.Field(&sp.Value, validation.Required))
}

//ValidateSETAttrParams validates an entire list of parameters. Applicable to SET commands
func ValidateSETAttrParams(params []SetParam) (err error) {
	if params == nil || len(params) == 0 {
		err = errors.New("invalid list of params")
		return
	}
	for _, param := range params {
		if err = validation.Validate(param.Attributes, validation.Required.Error("invalid attr")); err != nil {
			return
		}
	}
	return
}
