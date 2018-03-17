package translation

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
	Command    string   `json:"command"`
	Names      []string `json:"names"`
	Attributes string   `json:"attributes,omitempty"`
}
