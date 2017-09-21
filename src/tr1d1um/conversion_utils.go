package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-ozzo/ozzo-validation"
)

type Vars map[string]string

type ConversionTool interface {
	GetFlavorFormat(*http.Request, string, string, string) (*GetWDMP, error)
	SetFlavorFormat(*http.Request) (*SetWDMP, error)
	DeleteFlavorFormat(Vars, string) (*DeleteRowWDMP, error)
	AddFlavorFormat(io.Reader, Vars, string) (*AddRowWDMP, error)
	ReplaceFlavorFormat(io.Reader, Vars, string) (*ReplaceRowsWDMP, error)

	ValidateAndDeduceSET(http.Header, *SetWDMP) error
	GetFromUrlPath(string, Vars) (string, bool)
	GetConfiguredWrp([]byte, Vars, http.Header) *wrp.Message
}

type EncodingTool interface {
	GenericEncode(interface{}, wrp.Format) ([]byte, error)
	DecodeJSON(io.Reader, interface{}) error
	EncodeJSON(interface{}) ([]byte, error)
	ExtractPayload(io.Reader, wrp.Format) ([]byte, error)
}

type EncodingHelper struct{}
type ConversionWdmp struct {
	encodingHelper EncodingTool
}

/* The following functions break down the different cases for requests (https://swagger.webpa.comcast.net/)
containing WDMP content. Their main functionality is to attempt at reading such content, validating it
and subsequently returning a json type encoding of it. Most often this resulting []byte is used as payload for
wrp messages
*/
func (cw *ConversionWdmp) GetFlavorFormat(req *http.Request, attr, namesKey, sep string) (wdmp *GetWDMP, err error) {
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

func (cw *ConversionWdmp) SetFlavorFormat(req *http.Request) (wdmp *SetWDMP, err error) {
	wdmp = new(SetWDMP)

	if err = cw.encodingHelper.DecodeJSON(req.Body, wdmp); err == nil {
		err = cw.ValidateAndDeduceSET(req.Header, wdmp)
	}
	return
}

func (cw *ConversionWdmp) DeleteFlavorFormat(urlVars Vars, rowKey string) (wdmp *DeleteRowWDMP, err error) {
	wdmp = &DeleteRowWDMP{Command: COMMAND_DELETE_ROW}

	if row, exists := cw.GetFromUrlPath(rowKey, urlVars); exists && row != "" {
		wdmp.Row = row
	} else {
		err = errors.New("non-empty row name is required")
		return
	}
	return
}

func (cw *ConversionWdmp) AddFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *AddRowWDMP, err error) {
	wdmp = &AddRowWDMP{Command: COMMAND_ADD_ROW}

	if table, exists := cw.GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	if err = cw.encodingHelper.DecodeJSON(input, &wdmp.Row); err == nil {
		err = validation.Validate(wdmp.Row, validation.Required)
	}

	return
}

func (cw *ConversionWdmp) ReplaceFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *ReplaceRowsWDMP, err error) {
	wdmp = &ReplaceRowsWDMP{Command: COMMAND_REPLACE_ROWS}

	if table, exists := cw.GetFromUrlPath(tableName, urlVars); exists {
		wdmp.Table = table
	} else {
		err = errors.New("tableName is required for this method")
		return
	}

	if err = cw.encodingHelper.DecodeJSON(input, &wdmp.Rows); err == nil {
		err = validation.Validate(wdmp.Rows, validation.Required)
	}

	return
}

//This method attempts at defaulting to the SET command given that all the command property requirements are satisfied.
// (name, value, dataType). Then, if the new_cid is provided, it is deduced that the command should be TEST_SET
//else,
func (cw *ConversionWdmp) ValidateAndDeduceSET(header http.Header, wdmp *SetWDMP) (err error) {
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

// Same as invoking urlVars[key] directly but urlVars can be nil in which case key does not exist in it
func (cw *ConversionWdmp) GetFromUrlPath(key string, urlVars Vars) (val string, exists bool) {
	if urlVars != nil {
		val, exists = urlVars[key]
	}
	return
}

//Set the necessary fields in the wrp and return it
func (cw *ConversionWdmp) GetConfiguredWrp(wdmp []byte, pathVars Vars, header http.Header) (wrpMsg *wrp.Message) {
	deviceID, _ := cw.GetFromUrlPath("deviceid", pathVars)
	service, _ := cw.GetFromUrlPath("service", pathVars)

	wrpMsg = &wrp.Message{
		Type:            wrp.SimpleRequestResponseMessageType,
		ContentType:     header.Get("Content-Type"),
		Payload:         wdmp,
		Source:          WRP_SOURCE + "/" + service,
		Destination:     deviceID + "/" + service,
		TransactionUUID: header.Get(HEADER_WPA_TID),
	}
	return
}

/*   Encoding Helper methods below */

//Decodes data from the input into v
//Uses json.Unmarshall to perform actual decoding
func (helper *EncodingHelper) DecodeJSON(input io.Reader, v interface{}) (err error) {
	var payload []byte
	if payload, err = ioutil.ReadAll(input); err == nil {
		err = json.Unmarshal(payload, v)
	}
	return
}

//Wrapper function for json.Marshall
func (helper *EncodingHelper) EncodeJSON(v interface{}) (data []byte, err error) {
	data, err = json.Marshal(v)
	return
}

//Given an encoded wrp message, decode it and return its payload
func (helper *EncodingHelper) ExtractPayload(input io.Reader, format wrp.Format) (payload []byte, err error) {
	wrpResponse := &wrp.Message{}

	if err = wrp.NewDecoder(input, format).Decode(wrpResponse); err == nil {
		payload = wrpResponse.Payload
	}

	return
}

//Wraps common WRP encoder. Using a temporary buffer, simply returns
//encoded data and error when applicable
func (helper *EncodingHelper) GenericEncode(v interface{}, f wrp.Format) (data []byte, err error) {
	var tmp bytes.Buffer
	err = wrp.NewEncoder(&tmp, f).Encode(v)
	data = tmp.Bytes()
	return
}
