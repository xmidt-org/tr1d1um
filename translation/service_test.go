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
	}

	var contentTypeValue, authHeaderValue string

	w.RetryDo = func(r *http.Request) (*http.Response, error) {
		contentTypeValue, authHeaderValue = r.Header.Get(contentTypeHeaderKey), r.Header.Get(authHeaderKey)
		return &http.Response{Header: http.Header{}}, nil
	}

	resp, err := w.SendWRP(wrpMsg, "auth", "test")

	assert.Nil(err)

	//verify correct header values are set in request
	assert.EqualValues(wrp.Msgpack.ContentType(), contentTypeValue)
	assert.EqualValues("auth", authHeaderValue)

	//verify tid is set in response header
	assert.EqualValues("tid", resp.Header.Get(HeaderWPATID))
}
