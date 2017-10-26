/**
 * Copyright 2017 Comcast Cable Communications Management, LLC
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 */

package main

import (
	"io"
	"net/http"

	"time"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/mock"
)

/*  Mocks for ConversionTool  */

type MockConversionTool struct {
	mock.Mock
}

func (m *MockConversionTool) GetFlavorFormat(req *http.Request, vars Vars, i string, i2 string, i3 string) (*GetWDMP, error) {
	args := m.Called(req, vars, i, i2, i3)
	return args.Get(0).(*GetWDMP), args.Error(1)
}

func (m *MockConversionTool) SetFlavorFormat(req *http.Request) (*SetWDMP, error) {
	args := m.Called(req)
	return args.Get(0).(*SetWDMP), args.Error(1)
}

func (m *MockConversionTool) DeleteFlavorFormat(vars Vars, i string) (*DeleteRowWDMP, error) {
	args := m.Called(vars, i)
	return args.Get(0).(*DeleteRowWDMP), args.Error(1)
}

func (m *MockConversionTool) AddFlavorFormat(input io.Reader, vars Vars, i string) (*AddRowWDMP, error) {
	args := m.Called(input, vars, i)
	return args.Get(0).(*AddRowWDMP), args.Error(1)
}

func (m *MockConversionTool) ReplaceFlavorFormat(input io.Reader, vars Vars, i string) (*ReplaceRowsWDMP, error) {
	args := m.Called(input, vars, i)
	return args.Get(0).(*ReplaceRowsWDMP), args.Error(1)
}

func (m *MockConversionTool) ValidateAndDeduceSET(header http.Header, wdmp *SetWDMP) error {
	args := m.Called(header, wdmp)
	return args.Error(0)
}

func (m *MockConversionTool) GetFromURLPath(key string, vars Vars) (string, bool) {
	args := m.Called(key, vars)
	return args.String(0), args.Bool(1)
}

func (m *MockConversionTool) GetConfiguredWRP(wdmp []byte, pathVars Vars, header http.Header) (wrpMsg *wrp.Message) {
	args := m.Called(wdmp, pathVars, header)
	return args.Get(0).(*wrp.Message)
}

/*  Mocks for EncodingTool  */
type MockEncodingTool struct {
	mock.Mock
}

func (m *MockEncodingTool) GenericEncode(v interface{}, f wrp.Format) ([]byte, error) {
	args := m.Called(v, f)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEncodingTool) DecodeJSON(input io.Reader, v interface{}) error {
	args := m.Called(input, v)
	return args.Error(0)
}

func (m *MockEncodingTool) EncodeJSON(v interface{}) ([]byte, error) {
	args := m.Called(v)
	return args.Get(0).([]byte), args.Error(1)
}

func (m *MockEncodingTool) ExtractPayload(input io.Reader, format wrp.Format) ([]byte, error) {
	args := m.Called(input, format)
	return args.Get(0).([]byte), args.Error(1)
}

/* Mocks for SendAndHandle */

type MockSendAndHandle struct {
	mock.Mock
}

func (m *MockSendAndHandle) Send(ch *ConversionHandler, origin http.ResponseWriter, data []byte, req *http.Request) (*http.Response, error) {
	args := m.Called(ch, origin, data, req)
	return args.Get(0).(*http.Response), args.Error(1)
}
func (m *MockSendAndHandle) HandleResponse(ch *ConversionHandler, err error, resp *http.Response, origin http.ResponseWriter) {
	m.Called(ch, err, resp, origin)
}
func (m *MockSendAndHandle) GetRespTimeout() time.Duration {
	args := m.Called()
	return args.Get(0).(time.Duration)
}
