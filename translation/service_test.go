package translation

import (
	"net/http"
	"testing"
	"time"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/assert"
)

// testing SendWRP
type S struct {
}

func TestSendWRP(t *testing.T) {
	assert := assert.New(t)
	w := &WRPService{
		XmidtURL:   "http://localhost:8090/api/v2",
		CtxTimeout: time.Second,
		WRPSource:  "local",
	}

	wrpMsg := &wrp.Message{
		TransactionUUID: "tid",
		Source:          "test",
	}

	var (
		contentTypeValue, authHeaderValue string
		sentWRP                           = &wrp.Message{}
	)

	//capture sent values of interest
	w.RetryDo = func(r *http.Request) (resp *http.Response, err error) {

		if err = wrp.NewDecoder(r.Body, wrp.Msgpack).Decode(sentWRP); err == nil {
			contentTypeValue, authHeaderValue = r.Header.Get(contentTypeHeaderKey), r.Header.Get(authHeaderKey)
			resp = &http.Response{Header: http.Header{}}
			return
		}
		return
	}

	resp, err := w.SendWRP(wrpMsg, "auth")

	assert.Nil(err)

	//verify correct header values are set in request
	assert.EqualValues(wrp.Msgpack.ContentType(), contentTypeValue)
	assert.EqualValues("auth", authHeaderValue)

	//verify tid is set in response header
	assert.EqualValues("tid", resp.Header.Get(HeaderWPATID))

	//verify source in WRP message
	assert.EqualValues("local/test", sentWRP.Source)
}
