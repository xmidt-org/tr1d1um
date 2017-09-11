package main

import "net/http"
import (
	"github.com/Comcast/webpa-common/wrp"
	"io"
	"errors"
	"fmt"
)

//converts 'req' into its wrp equivalent
//input 'readFullBody' should read all of the body of the request
// In most cases, 'readFullBody' should simply be ioutil.ReadAll()

func HttpRequestToWRP(req *http.Request, readFullBody func(io.Reader)([]byte, error)) (message *wrp.Message, err error){
	if req == nil || readFullBody == nil {
		err = errors.New(fmt.Sprintf("Nil argument error. Req is nil: %v. readFullBody is nil: %v", req==nil,
			readFullBody==nil))
		return
	}

	message, err = wrp.HeaderToWRP(req.Header)
	if err != nil {
		return
	}

	message.Payload, err = readFullBody(req.Body)

	return
}
