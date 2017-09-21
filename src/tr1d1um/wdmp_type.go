package main

import (
	"errors"
	"github.com/go-ozzo/ozzo-validation"
)

const (
	COMMAND_GET          = "GET"
	COMMAND_GET_ATTRS    = "GET_ATTRIBUTES"
	COMMAND_SET          = "SET"
	COMMAND_SET_ATTRS    = "SET_ATTRIBUTES"
	COMMAND_TEST_SET     = "TEST_AND_SET"
	COMMAND_ADD_ROW      = "ADD_ROW"
	COMMAND_DELETE_ROW   = "DELETE_ROW"
	COMMAND_REPLACE_ROWS = "REPLACE_ROWS"

	HEADER_WPA_SYNC_OLD_CID = "X-Webpa-Sync-Old-Cid"
	HEADER_WPA_SYNC_NEW_CID = "X-Webpa-Sync-New-Cid"
	HEADER_WPA_SYNC_CMC     = "X-Webpa-Sync-Cmc"
	HEADER_WPA_TID          = "X-WebPA-Transaction-Id"

	ERR_UNSUCCESSFUL_DATA_PARSE = "Unsuccessful Data Parse"

	WRP_SOURCE = "dns:tr1d1um.webpa.comcast.net"
)

/*
	GET-Flavored structs
*/

type GetWDMP struct {
	Command   string   `json:"command"`
	Names     []string `json:"names,omitempty"`
	Attribute string   `json:"attributes,omitempty"`
}

/*
	SET-Flavored structs
*/

type Attr map[string]interface{}
type IndexRow map[string]map[string]string

type SetParam struct {
	Name       *string     `json:"name"`
	DataType   *int8       `json:"dataType,omitempty"`
	Value      interface{} `json:"value,omitempty"`
	Attributes Attr        `json:"attributes,omitempty"`
}

type SetWDMP struct {
	Command    string     `json:"command"`
	OldCid     string     `json:"old-cid,omitempty"`
	NewCid     string     `json:"new-cid,omitempty"`
	SyncCmc    string     `json:"sync-cmc,omitempty"`
	Parameters []SetParam `json:"parameters,omitempty"`
}

/*
	Row-related command structs
*/

type AddRowWDMP struct {
	Command string            `json:"command"`
	Table   string            `json:"table"`
	Row     map[string]string `json:"row"`
}

type ReplaceRowsWDMP struct {
	Command string   `json:"command"`
	Table   string   `json:"table"`
	Rows    IndexRow `json:"rows"`
}

type DeleteRowWDMP struct {
	Command string `json:"command"`
	Row     string `json:"row"`
}

/* Validation functions */

//Applicable for the SET and TEST_SET
func (sp SetParam) Validate() error {
	return validation.ValidateStruct(&sp,
		validation.Field(&sp.Name, validation.NotNil),
		validation.Field(&sp.DataType, validation.NotNil),
		validation.Field(&sp.Value, validation.Required))
}

func ValidateSETAttrParams(params []SetParam) (err error) {
	if params == nil || len(params) == 0 {
		return errors.New("invalid list of params")
	}
	for _, param := range params {
		if err = validation.Validate(param.Attributes, validation.Required.Error("invalid attr")); err != nil {
			return
		}
	}
	return
}
