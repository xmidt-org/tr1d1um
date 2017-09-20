package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-ozzo/ozzo-validation"
	"io"
	"io/ioutil"
	"net/http"
	"strings"
)

type Vars map[string]string
type BodyReader func(io.Reader) ([]byte, error)

/* The following functions break down the different cases for requests (https://swagger.webpa.comcast.net/)
containing WDMP content. Their main functionality is to attempt at reading such content, validating it
and subsequently returning a json type encoding of it. Most often this resulting []byte is used as payload for
wrp messages
*/
func GetFlavorFormat(req *http.Request, attr, namesKey, sep string) (wdmp *GetWDMP, err error) {
	wdmp = new(GetWDMP)

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

	return
}

func SetFlavorFormat(req *http.Request) (wdmp *SetWDMP, err error) {
	wdmp = new(SetWDMP)

	if err = DecodeJson(req.Body, wdmp); err == nil {
		err = ValidateAndDeduceSET(req.Header, wdmp)
	}
	return
}

func DeleteFlavorFormat(urlVars Vars, rowKey string) (wdmp *DeleteRowWDMP, err error) {
	wdmp = &DeleteRowWDMP{Command: COMMAND_DELETE_ROW}

	if row, exists := GetFromUrlPath(rowKey, urlVars); exists && row != "" {
		wdmp.Row = row
	} else {
		err = errors.New("non-empty row name is required")
		return
	}
	return
}

func AddFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *AddRowWDMP, err error) {
	wdmp = &AddRowWDMP{Command: COMMAND_ADD_ROW}

	if table, exists := GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	if err = DecodeJson(input, &wdmp.Row); err == nil {
		err = validation.Validate(wdmp.Row, validation.Required)
	}

	return
}

func ReplaceFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *ReplaceRowsWDMP, err error) {
	wdmp = &ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS}

	if table, exists := GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	if err = DecodeJson(input, &wdmp.Rows); err == nil {
		err = validation.Validate(wdmp.Rows, validation.Required)
	}

	return
}

//This method attempts at defaulting to the SET command given that all the command property requirements are satisfied.
// (name, value, dataType). Then, if the new_cid is provided, it is deduced that the command should be TEST_SET
//else,
func ValidateAndDeduceSET(header http.Header, wdmp *SetWDMP) (err error) {
	if err = validation.Validate(wdmp.Parameters, validation.Required); err == nil {
		wdmp.Command = COMMAND_SET
		if newCid := header.Get(HEADER_WPA_SYNC_NEW_CID); newCid != "" {
			wdmp.OldCid, wdmp.NewCid = header.Get(HEADER_WPA_SYNC_OLD_CID), newCid

			if syncCmc := header.Get(HEADER_WPA_SYNC_CMC); syncCmc != "" {
				wdmp.SyncCmc = syncCmc
			}
			wdmp.Command = COMMAND_TEST_SET
		}
	} else {
		errMsg := err.Error()
		if !(errMsg == "cannot be blank" || strings.Contains(errMsg, "name")) {
			if err = ValidateSETAttrParams(wdmp.Parameters); err == nil {
				wdmp.Command = COMMAND_SET_ATTRS
			}
		}
	}
	return
}

//Decodes data from the input into v
//Uses json.Unmarshall to perform actual decoding
func DecodeJson(input io.Reader, v interface{}) (err error) {
	var payload []byte
	if payload, err = ioutil.ReadAll(input); err == nil {
		err = json.Unmarshal(payload, v)
	}
	return
}

//Wrapper function for json.Marshall
func EncodeJson(v interface{}) (data []byte, err error) {
	data, err = json.Marshal(v)
	return
}

// Same as invoking urlVars[key] directly but urlVars can be nil in which case key does not exist in it
func GetFromUrlPath(key string, urlVars Vars) (val string, exists bool) {
	if urlVars != nil {
		val, exists = urlVars[key]
	}
	return
}

//Given an encoded wrp message, decode it and return its payload
func ExtractPayload(input io.Reader, format wrp.Format) (payload []byte, err error) {
	wrpResponse := &wrp.Message{}

	if err = wrp.NewDecoder(input, format).Decode(wrpResponse); err == nil {
		payload = wrpResponse.Payload
	}

	return
}

//Wraps common WRP encoder. Using a temporary buffer, simply returns
//encoded data and error when applicable
func GenericEncode(v interface{}, f wrp.Format) (data []byte, err error) {
	var tmp bytes.Buffer
	err = wrp.NewEncoder(&tmp, f).Encode(v)
	data = tmp.Bytes()
	return
}
