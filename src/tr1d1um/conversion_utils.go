package main

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"strings"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/go-ozzo/ozzo-validation"
)

//Vars shortens frequently used type returned by mux.Vars()
type Vars map[string]string

//ConversionTool lays out the definition of methods to build WDMP from content in an http request
type ConversionTool interface {
	GetFlavorFormat(*http.Request, Vars, string, string, string) (*GetWDMP, error)
	SetFlavorFormat(*http.Request) (*SetWDMP, error)
	DeleteFlavorFormat(Vars, string) (*DeleteRowWDMP, error)
	AddFlavorFormat(io.Reader, Vars, string) (*AddRowWDMP, error)
	ReplaceFlavorFormat(io.Reader, Vars, string) (*ReplaceRowsWDMP, error)

	ValidateAndDeduceSET(http.Header, *SetWDMP) error
	GetFromURLPath(string, Vars) (string, bool)
	GetConfiguredWRP([]byte, Vars, http.Header) *wrp.Message
}

//EncodingTool lays out the definition of methods used for encoding/decoding between WDMP and WRP
type EncodingTool interface {
	GenericEncode(interface{}, wrp.Format) ([]byte, error)
	DecodeJSON(io.Reader, interface{}) error
	EncodeJSON(interface{}) ([]byte, error)
	ExtractPayload(io.Reader, wrp.Format) ([]byte, error)
}

//EncodingHelper implements the definitions defined in EncodingTool
type EncodingHelper struct{}

//ConversionWDMP implements the definitions defined in ConversionTool
type ConversionWDMP struct {
	encodingHelper EncodingTool
}

//The following functions with names of the form {command}FlavorFormat serve as the low level builders of WDMP objects
// based on the commands they serve for Cloud <-> TR181 devices communication

//GetFlavorFormat constructs a WDMP object out of the contents of a given request. Supports the GET command
func (cw *ConversionWDMP) GetFlavorFormat(req *http.Request, pathVars Vars, attr, namesKey, sep string) (wdmp *GetWDMP, err error) {
	wdmp = new(GetWDMP)

	if service, _ := cw.GetFromURLPath("service", pathVars); service == "stat" {
		return
		//todo: maybe we need more validation here
	}

	if nameGroup := req.FormValue(namesKey); nameGroup != "" {
		wdmp.Command = CommandGet
		wdmp.Names = strings.Split(nameGroup, sep)
	} else {
		err = errors.New("names is a required property for GET")
		return
	}

	if attributes := req.FormValue(attr); attributes != "" {
		wdmp.Command = CommandGetAttrs
		wdmp.Attribute = attributes
	}

	return
}

//SetFlavorFormat has analogous functionality to GetFlavorformat but instead supports the various SET commands
func (cw *ConversionWDMP) SetFlavorFormat(req *http.Request) (wdmp *SetWDMP, err error) {
	wdmp = new(SetWDMP)

	if err = cw.encodingHelper.DecodeJSON(req.Body, wdmp); err == nil {
		err = cw.ValidateAndDeduceSET(req.Header, wdmp)
	}
	return
}

//DeleteFlavorFormat again has analogous functionality to GetFlavormat but for the DELETE_ROW command
func (cw *ConversionWDMP) DeleteFlavorFormat(urlVars Vars, rowKey string) (wdmp *DeleteRowWDMP, err error) {
	wdmp = &DeleteRowWDMP{Command: CommandDeleteRow}

	if row, exists := cw.GetFromURLPath(rowKey, urlVars); exists && row != "" {
		wdmp.Row = row
	} else {
		err = errors.New("non-empty row name is required")
		return
	}
	return
}

//AddFlavorFormat supports the ADD_ROW command
func (cw *ConversionWDMP) AddFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *AddRowWDMP, err error) {
	wdmp = &AddRowWDMP{Command: CommandAddRow}

	if table, exists := cw.GetFromURLPath(tableName, urlVars); exists {
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

//ReplaceFlavorFormat supports the REPLACE_ROWS command
func (cw *ConversionWDMP) ReplaceFlavorFormat(input io.Reader, urlVars Vars, tableName string) (wdmp *ReplaceRowsWDMP, err error) {
	wdmp = &ReplaceRowsWDMP{Command: CommandReplaceRows}

	if table, exists := cw.GetFromURLPath(tableName, urlVars); exists {
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

//ValidateAndDeduceSET attempts at defaulting to the SET command given all command property requirements are satisfied
// (name, value, dataType). Then, if the new_cid is provided, it is deduced that the command should be TEST_SET
//If the SET command properties are not satisfied, we attempt at validating the input for the SET_ATTRS command
func (cw *ConversionWDMP) ValidateAndDeduceSET(header http.Header, wdmp *SetWDMP) (err error) {
	if err = validation.Validate(wdmp.Parameters, validation.Required); err == nil {
		wdmp.Command = CommandSet
		if newCid := header.Get(HeaderWPASyncNewCID); newCid != "" {
			wdmp.OldCid, wdmp.NewCid = header.Get(HeaderWPASyncOldCID), newCid

			if syncCmc := header.Get(HeaderWPASyncCMC); syncCmc != "" {
				wdmp.SyncCmc = syncCmc
			}
			wdmp.Command = CommandTestSet
		}
	} else {
		errMsg := err.Error()
		if !(errMsg == "cannot be blank" || strings.Contains(errMsg, "name")) {
			if err = ValidateSETAttrParams(wdmp.Parameters); err == nil {
				wdmp.Command = CommandSetAttrs
			}
		}
	}
	return
}

//GetFromURLPath Same as invoking urlVars[key] directly but urlVars can be nil in which case key does not exist in it
func (cw *ConversionWDMP) GetFromURLPath(key string, urlVars Vars) (val string, exists bool) {
	if urlVars != nil {
		val, exists = urlVars[key]
	}
	return
}

//GetConfiguredWRP Set the necessary fields in the wrp and return it
func (cw *ConversionWDMP) GetConfiguredWRP(wdmp []byte, pathVars Vars, header http.Header) (wrpMsg *wrp.Message) {
	deviceID, _ := cw.GetFromURLPath("deviceid", pathVars)
	service, _ := cw.GetFromURLPath("service", pathVars)

	wrpMsg = &wrp.Message{
		Type:            wrp.SimpleRequestResponseMessageType,
		ContentType:     header.Get("Content-Type"),
		Payload:         wdmp,
		Source:          WRPSource + "/" + service,
		Destination:     deviceID + "/" + service,
		TransactionUUID: GetOrGenTID(header),
	}
	return
}

/*   Encoding Helper methods below */

//DecodeJSON decodes data from the input into v. It uses json.Unmarshall to perform actual decoding
func (helper *EncodingHelper) DecodeJSON(input io.Reader, v interface{}) (err error) {
	var payload []byte
	if payload, err = ioutil.ReadAll(input); err == nil {
		err = json.Unmarshal(payload, v)
	}
	return
}

//EncodeJSON wraps the json.Marshall method
func (helper *EncodingHelper) EncodeJSON(v interface{}) (data []byte, err error) {
	data, err = json.Marshal(v)
	return
}

//ExtractPayload decodes an encoded wrp message and returns its payload
func (helper *EncodingHelper) ExtractPayload(input io.Reader, format wrp.Format) (payload []byte, err error) {
	wrpResponse := &wrp.Message{Type: wrp.SimpleRequestResponseMessageType}

	if err = wrp.NewDecoder(input, format).Decode(wrpResponse); err == nil {
		payload = wrpResponse.Payload
	}

	return
}

//GenericEncode wraps a WRP encoder. Using a temporary buffer, simply returns the encoded data and error when applicable
func (helper *EncodingHelper) GenericEncode(v interface{}, f wrp.Format) (data []byte, err error) {
	var tmp bytes.Buffer
	err = wrp.NewEncoder(&tmp, f).Encode(v)
	data = tmp.Bytes()
	return
}

//GetOrGenTID returns a Transaction ID for a given request.
//If a TID was provided in the headers, such is used. Otherwise,
//a new TID is generated and returned
func GetOrGenTID(requestHeader http.Header) (tid string) {
	if tid = requestHeader.Get(HeaderWPATID); tid == "" {
		buf := make([]byte, 16)
		if _, err := rand.Read(buf); err == nil {
			tid = base64.RawURLEncoding.EncodeToString(buf)
		}
	}
	return
}
