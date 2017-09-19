package main

import (
	"encoding/json"
	"errors"
	"github.com/go-ozzo/ozzo-validation"
	"io"
	"net/http"
	"strings"
	"github.com/Comcast/webpa-common/wrp"
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

	if err = DecodeJsonPayload(req.Body, wdmp, ReadEntireBody); err != nil {
		return
	}

	if err = ValidateAndDeduceSET(req.Header, wdmp); err != nil {
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

func DeleteFlavorFormat(urlVars Vars, rowKey string) (payload []byte, err error) {
	wdmp := &DeleteRowWDMP{Command: COMMAND_DELETE_ROW}

	if row, exists := GetFromUrlPath(rowKey, urlVars); exists && row != "" {
		wdmp.Row = row
	} else {
		err = errors.New("non-empty row name is required")
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

	if err = DecodeJsonPayload(body, &wdmp.Row, ReadEntireBody); err != nil {
		return
	}

	if len(wdmp.Row) == 0 {
		err = errors.New("input data is empty")
		return
	}

	payload, err = json.Marshal(wdmp)
	return
}

func ReplaceFlavorFormat(body io.Reader, urlVars Vars, tableName string, ReadEntireBody BodyReader) (payload []byte, err error) {
	wdmp := &ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS}

	if table, exists := GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	if err = DecodeJsonPayload(body, &wdmp.Rows, ReadEntireBody); err != nil {
		return
	}

	if err = validation.Validate(wdmp.Rows, validation.Required); err != nil {
		return
	}

	payload, err = json.Marshal(wdmp)
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
			wdmp.SyncCmc = SetOrLeave(wdmp.SyncCmc, header.Get(HEADER_WPA_SYNC_CMC)) //field is optional
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

//if newVal is empty, the currentVal is return
//else newVal is return
func SetOrLeave(currentVal, newVal string) string {
	if validation.Validate(newVal, validation.Required) != nil {
		return currentVal
	}
	return newVal
}

func ExtractPayloadFromWrp(body io.Reader, ReadAll BodyReader) (payload []byte, err error) {
	wrpResponse := wrp.SimpleRequestResponse{Type: wrp.SimpleRequestResponseMessageType}
	if err = DecodeJsonPayload(body, wrpResponse, ReadAll); err != nil {
		return
	}
	payload = wrpResponse.Payload
	return
}
