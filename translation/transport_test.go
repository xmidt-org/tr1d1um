package translation

import (
	"encoding/json"
	"testing"

	"github.com/Comcast/webpa-common/wrp"

	"github.com/Comcast/webpa-common/device"

	"github.com/stretchr/testify/assert"
)

func TestRequestPayload(t *testing.T) {
	t.Run("EmptyNames", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("", "")
		assert.EqualValues(ErrEmptyNames, e)
		assert.Nil(p)
	})

	t.Run("GET", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("n0,n1", "")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&GetWDMP{Command: CommandGet, Names: []string{"n0", "n1"}})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})

	t.Run("GETAttrs", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestPayload("n0,n1", "attr0")
		assert.Nil(e)

		expectedBytes, err := json.Marshal(&GetWDMP{Command: CommandGetAttrs, Names: []string{"n0", "n1"}, Attributes: "attr0"})

		if err != nil {
			panic(err)
		}

		assert.EqualValues(expectedBytes, p)
	})
}

func TestWrapInWRP(t *testing.T) {
	t.Run("EmptyVars", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrapInWRP([]byte(""), "", nil)

		assert.Nil(w)
		assert.EqualValues(device.ErrorInvalidDeviceName, e)
	})

	t.Run("GivenTID", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrapInWRP([]byte{'t'}, "t0", map[string]string{"deviceid": "mac:112233445566", "service": "s0"})

		assert.Nil(e)
		assert.EqualValues(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.EqualValues([]byte{'t'}, w.Payload)
		assert.EqualValues("mac:112233445566/s0", w.Destination)
		assert.EqualValues("s0", w.Source)
		assert.EqualValues("t0", w.TransactionUUID)
	})

	t.Run("GeneratedTID", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrapInWRP([]byte{'t'}, "", map[string]string{"deviceid": "mac:112233445566", "service": "s0"})

		assert.Nil(e)
		assert.EqualValues(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.EqualValues([]byte{'t'}, w.Payload)
		assert.EqualValues("mac:112233445566/s0", w.Destination)
		assert.EqualValues("s0", w.Source)
		assert.NotEmpty(w.TransactionUUID)
	})
}

func TestEncode(t *testing.T) {

}
