package translation

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"net/http"

	"github.com/Comcast/webpa-common/device"
	"github.com/Comcast/webpa-common/wrp"
	"github.com/gorilla/mux"
)

func configWRP(WDMP []byte, r *http.Request, tid string) (m *wrp.Message, err error) {
	var (
		canonicalDeviceID device.ID
		pathVars          = mux.Vars(r)
	)

	deviceID, _ := pathVars["deviceid"]
	if canonicalDeviceID, err = device.ParseID(deviceID); err == nil {
		service, _ := pathVars["service"]

		m = &wrp.Message{
			Type:            wrp.SimpleRequestResponseMessageType,
			Payload:         WDMP,
			Destination:     fmt.Sprintf("%s/%s", string(canonicalDeviceID), service),
			TransactionUUID: tid,
		}
	}
	return
}

func genTID() (tid string, err error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}
