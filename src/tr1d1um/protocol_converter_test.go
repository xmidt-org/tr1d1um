package main

import (
	"testing"
	"net/http"
	"io"
	"errors"
	"github.com/stretchr/testify/assert"
	"bytes"
	"github.com/Comcast/webpa-common/wrp"
	"strings"
)

func TestHttpRequestToWRP(t *testing.T) {
	assert := assert.New(t)
	var ReadFullNonNull = func(body io.Reader) (readBody []byte, err error) {
		if body == nil {
			err = errors.New("We do not read nil bodies!")
			return
		}
		readBody = []byte("theBody")
		return
	}

	req, _ := http.NewRequest("GET", "someurl", bytes.NewBufferString(""))
	req.Header.Add(wrp.MsgTypeHeader, wrp.AuthMessageType.String())

	t.Run("GoodConversion", func (t *testing.T){
		wrpMessage, err := HttpRequestToWRP(req, ReadFullNonNull)
		assert.NotNil(wrpMessage)
		assert.Nil(err)
		assert.EqualValues(wrp.AuthMessageType, wrpMessage.Type)
	})

	t.Run("HeaderIssue", func (t *testing.T){
		//delete Header
		req.Header.Del(wrp.MsgTypeHeader)

		wrpMessage, err := HttpRequestToWRP(req, ReadFullNonNull)
		assert.Nil(wrpMessage)
		assert.NotNil(err)
		assert.EqualValues(err.Error(), "Invalid Message Type")
	})

	// Messed up http payload
	t.Run("PayloadIssue", func (t *testing.T){
		//add required header back
		req.Header.Add(wrp.MsgTypeHeader, wrp.AuthMessageType.String())

		//force body failure
		req.Body = nil

		wrpMessage, err := HttpRequestToWRP(req, ReadFullNonNull)
		assert.NotNil(wrpMessage)
		assert.NotNil(err)
		assert.True(strings.HasSuffix(err.Error(), "nil bodies!"))
	})

	// Nil argument case
	t.Run("NilArgumentCase", func (t *testing.T){
		wrpMessage, err := HttpRequestToWRP(nil, ReadFullNonNull)
		assert.Nil(wrpMessage)
		assert.NotNil(err)
		assert.Contains(err.Error(), "nil")
	})
}




