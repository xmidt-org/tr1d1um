package main

import (
	"net/http"
	"time"
	"io/ioutil"
)

type ConversionHandler struct {
	//todo add loggers
	timeOut time.Duration
	targetUlr string
}

func (sh *ConversionHandler) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	_ , err := HttpRequestToWRP(request, ioutil.ReadAll)
	if err != nil {
		response.WriteHeader(http.StatusBadRequest)
		response.Write([]byte(err.Error()))
		return
	}

	//todo forward converted wrp request to targetUrl with timeout
}
