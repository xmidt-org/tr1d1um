package main

const(
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

	ERR_UNSUCCESSFUL_DATA_PARSE = "Unsuccessful Data Parse"
	ERR_UNSUCCESSFUL_DATA_WRAP = "Unsuccessful WDMP data transfer into wrp message"

)
/*
	GET-Flavored structs
*/

type GetWDMP struct {
	Command    string   `json:"command"`
	Names      []string `json:"names,omitempty"`
	Attribute string    `json:"attributes,omitempty"`
}

/*
	SET-Flavored structs
*/

type Attr map[string]interface{}

type SetParam struct {
	Name*      string      `json:"name"`
	DataType*   int32      `json:"dataType,omitempty"`
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
	Command string                       `json:"command"`
	Table   string                       `json:"table"`
	Rows    map[string]map[string]string `json:"rows"`
}

type DeleteRowWDMP struct {
	Command string `json:"command"`
	Row     string `json:"row"`
}

