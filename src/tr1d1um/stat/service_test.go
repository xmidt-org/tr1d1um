package stat

import (
	"net/http"
	"testing"
	"tr1d1um/common"

	"github.com/stretchr/testify/mock"
)

func TestRequestStat(t *testing.T) {
	m := new(common.MockTr1d1umTransactor)
	s := NewService(
		&ServiceOptions{
			XmidtStatURL:      "http://localhost/stat/${device}",
			Tr1d1umTransactor: m,
		})

	var requestMatcher = func(r *http.Request) bool {
		return r.URL.String() == "http://localhost/stat/mac:1122334455" &&
			r.Header.Get("Authorization") == "token"
	}

	m.On("Transact", mock.MatchedBy(requestMatcher)).Return(&common.XmidtResponse{}, nil)

	s.RequestStat("token", "mac:1122334455")
}
