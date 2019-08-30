package translation

import (
	"io/ioutil"
	"net/http"
	"testing"

	"github.com/xmidt-org/tr1d1um/common"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/xmidt-org/wrp-go/wrp"
)

func TestSendWRP(t *testing.T) {
	assert := assert.New(t)
	m := new(common.MockTr1d1umTransactor)

	s := NewService(&ServiceOptions{
		XmidtWrpURL:       "http://localhost/wrp",
		WRPSource:         "local",
		Tr1d1umTransactor: m,
	})

	var expected = wrp.MustEncode(wrp.Message{
		Type:   wrp.SimpleRequestResponseMessageType,
		Source: "local/test",
	}, wrp.Msgpack)

	var argMatcher = func(r *http.Request) bool {
		assert.EqualValues("token", r.Header.Get("Authorization"))
		assert.EqualValues(wrp.Msgpack.ContentType(), r.Header.Get("Content-Type"))

		data, _ := ioutil.ReadAll(r.Body)

		assert.EqualValues(string(expected), string(data))

		//MatchedBy is not friendly in explicitly showing what's not matching
		//so we use assertions instead in this function
		return true
	}

	m.On("Transact", mock.MatchedBy(argMatcher)).Return(nil, nil)
	_, e := s.SendWRP(
		&wrp.Message{
			Type:   wrp.SimpleRequestResponseMessageType,
			Source: "test",
		}, "token")

	assert.Nil(e)
}
