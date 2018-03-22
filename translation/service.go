package translation

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/Comcast/webpa-common/wrp"
)

//Service defines the behavior for the Translation WRP Service
type Service interface {
	SendWRP(*wrp.Message, string) (*http.Response, error)
}

//WRPService represents the Webpa-Tr1d1um service that sends WRP data to the XMiDT cluster
type WRPService struct {
	//RetryDo is http.Client.Do with multiple retries supported
	RetryDo func(*http.Request) (*http.Response, error)

	//URL of the XMiDT Service for WRP messages
	WrpXmidtURL string

	//CtxTimeout is the timeout for any given HTTP transaction
	CtxTimeout time.Duration

	//WRPSource is currently the prefix of "Source" field in outgoing WRP Messages
	WRPSource string
}

//SendWRP sends the given wrpMsg to the XMiDT cluster and returns the *http.Response
func (w *WRPService) SendWRP(wrpMsg *wrp.Message, authValue string) (resp *http.Response, err error) {
	var payload []byte

	// fill in the rest of the source property
	wrpMsg.Source = fmt.Sprintf("%s/%s", w.WRPSource, wrpMsg.Source)

	if err = wrp.NewEncoderBytes(&payload, wrp.Msgpack).Encode(wrpMsg); err == nil {
		var req *http.Request
		if req, err = http.NewRequest(http.MethodPost, w.WrpXmidtURL, bytes.NewBuffer(payload)); err == nil {

			req.Header.Add(contentTypeHeaderKey, wrp.Msgpack.ContentType())
			req.Header.Add(authHeaderKey, authValue)

			ctx, cancel := context.WithTimeout(req.Context(), w.CtxTimeout)
			defer cancel()

			resp, err = w.RetryDo(req.WithContext(ctx))

			//place TID in response
			resp.Header.Set(http.CanonicalHeaderKey(HeaderWPATID), wrpMsg.TransactionUUID)
		}
	}
	return
}
