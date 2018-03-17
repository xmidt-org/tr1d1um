package translation

import (
	"crypto/rand"
	"encoding/base64"
)

func genTID() (tid string, err error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err == nil {
		tid = base64.RawURLEncoding.EncodeToString(buf)
	}
	return
}
