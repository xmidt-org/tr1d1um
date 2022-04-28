/**
 * Copyright 2022 Comcast Cable Communications Management, LLC
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

package translation

// All the supported commands, WebPA Headers and misc
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
)

type getWDMP struct {
	Command    string   `json:"command"`
	Names      []string `json:"names"`
	Attributes string   `json:"attributes,omitempty"`
}
type setWDMP struct {
	Command    string     `json:"command"`
	OldCid     string     `json:"old-cid,omitempty"`
	NewCid     string     `json:"new-cid,omitempty"`
	SyncCmc    string     `json:"sync-cmc,omitempty"`
	Parameters []setParam `json:"parameters,omitempty"`
}

type setParam struct {
	Name       *string                `json:"name"`
	DataType   *int8                  `json:"dataType,omitempty"`
	Value      interface{}            `json:"value,omitempty"`
	Attributes map[string]interface{} `json:"attributes,omitempty"`
}

type addRowWDMP struct {
	Command string            `json:"command"`
	Table   string            `json:"table"`
	Row     map[string]string `json:"row"`
}

// indexRow facilitates data transfer from json data of the form {index1: {key:val}, index2: {key:val}, ... }
type indexRow map[string]map[string]string

// replaceRowsWDMP serves as container for data used for the REPLACE_ROWS command
type replaceRowsWDMP struct {
	Command string   `json:"command"`
	Table   string   `json:"table"`
	Rows    indexRow `json:"rows"`
}

type deleteRowDMP struct {
	Command string `json:"command"`
	Row     string `json:"row"`
}
