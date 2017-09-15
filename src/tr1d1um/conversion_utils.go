package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
)

var (
	ErrJsonEmpty = errors.New("JSON payload is empty")
)

type BodyReader func(io.Reader) ([]byte, error)

type Vars map[string]string

/* The following functions break down the different cases for requests (https://swagger.webpa.comcast.net/)
containing WDMP content. Their main functionality is to attempt at reading such content, validating it
and subsequently returning a json type encoding of it. Most often this resulting []byte is used as payload for
wrp messages
*/
func GetFlavorFormat(req *http.Request, attr, namesKey, sep string) (payload []byte, err error) {
	wdmp := new(GetWDMP)

	if nameGroup := req.FormValue(namesKey); nameGroup != "" {
		wdmp.Command = COMMAND_GET
		wdmp.Names = strings.Split(nameGroup, sep)
	} else {
		err = errors.New("names is a required property for GET")
		return
	}

	if attributes := req.FormValue(attr); attributes != "" {
		wdmp.Command = COMMAND_GET_ATTRS
		wdmp.Attribute = attributes
	}

	payload, err = json.Marshal(wdmp)
	return
}

func SetFlavorFormat(req *http.Request, ReadEntireBody BodyReader) (payload []byte, err error) {
	wdmp := new(SetWDMP)
	DecodeJsonPayload(req.Body, wdmp, ReadEntireBody)

	wdmp.Command, err = ValidateAndRetrieveCommand(req.Header, wdmp)

	if err != nil {
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

func DeleteFlavorFormat(urlVars Vars, rowKey string) (payload []byte, err error) {
	wdmp := &DeleteRowWDMP{Command: COMMAND_DELETE_ROW}

	if row, exists := GetFromUrlPath(rowKey, urlVars); exists {
		wdmp.Row = row
	} else {
		err = errors.New("row name is required")
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

func AddFlavorFormat(body io.Reader, urlVars Vars, tableName string, ReadEntireBody BodyReader) (payload []byte, err error) {
	wdmp := &AddRowWDMP{Command: COMMAND_ADD_ROW}

	if table, exists := GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}
	err = DecodeJsonPayload(body, wdmp.Row, ReadEntireBody)

	if err != nil {
		return
	}

	if len(wdmp.Row) == 0 {
		err = errors.New("input data is empty")
	}

	payload, err = json.Marshal(wdmp)
	return
}

func ReplaceFlavorFormat(body io.Reader, urlVars Vars, tableName string, ReadEntireBody BodyReader) (payload []byte, err error) {
	wdmp := ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS}

	if table, exists := GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	err = DecodeJsonPayload(body, &wdmp.Rows, ReadEntireBody)

	if err != nil {
		return
	}

	if !ValidREPLACEParams(wdmp.Rows) {
		err = errors.New("invalid Replacement data")
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

/* Validation functions */
func ValidateAndRetrieveCommand(header http.Header, wdmp *SetWDMP) (command string, err error) {
	if newCid := header.Get(HEADER_WPA_SYNC_NEW_CID); newCid != "" {
		wdmp.OldCid = header.Get(HEADER_WPA_SYNC_OLD_CID)
		wdmp.NewCid = newCid
		wdmp.SyncCmc = header.Get(HEADER_WPA_SYNC_CMC)
		command, err = ValidateSETParams(false, wdmp, COMMAND_TEST_SET)
	} else {
		command, err = ValidateSETParams(true, wdmp, "")
	}
	return
}

//  -Inputs-:
// **checkingForSetAttr**: true if we're checking for the required parameter properties for the SET_ATTRIBUTES command
//		These properties are: attributes and name
//
// **wdmp**: the WDMP object from which we retrieve the parameters
//
// **override**: overrides the final suggested command if non-empty. Useful if one just wants to check for SET command
//		parameter properties (value, dataType, name)
//
// 	-Outputs-:
// *command**: the final command based on the analysis of the parameters
// **err**: it is non-nil if any required property is violated
func ValidateSETParams(checkingForSetAttr bool, wdmp *SetWDMP, override string) (command string, err error) {
	for _, sp := range wdmp.Parameters {
		if sp.Name == nil {
			err = errors.New("name is required for parameters")
			return
		}

		if checkingForSetAttr {
			if sp.Value != nil || sp.Attributes == nil {
				checkingForSetAttr = false
			}
		} else { //in this case, we are just checking valid parameters for SET
			if sp.DataType == nil || sp.Value == nil {
				err = errors.New("dataType and value are required for SET command")
			}
		}
	}

	if override != "" {
		command = override
		return
	}

	if checkingForSetAttr { // checked for SET_ATTRS properties until the end and found no violation
		command = COMMAND_SET_ATTRS
		return
	}

	command = COMMAND_SET
	return
}

//Validate non-Empty mapping A (nonEmpty keys -> non-Empty(mapping B (string -> string))
func ValidREPLACEParams(rows map[string]map[string]string) (valid bool) {
	for k, v := range rows {
		if k == "" || v == nil || len(v) == 0 {
			return
		}
	}
	if len(rows) > 0 {
		valid = true
	}
	return
}

/* Other helper functions */
func DecodeJsonPayload(body io.Reader, v interface{}, ReadEntireBody BodyReader) (err error) {
	payload, err := ReadEntireBody(body)

	if err != nil {
		return
	}

	if len(payload) == 0 {
		err = ErrJsonEmpty
		return
	}

	err = json.Unmarshal(payload, v)
	return
}

func GetFromUrlPath(key string, urlVars map[string]string) (val string, exists bool) {
	if urlVars != nil {
		val, exists = urlVars[key]
	}
	return
}
