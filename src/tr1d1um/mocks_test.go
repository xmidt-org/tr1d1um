package main

import (
	"io"
	"net/http"

	"github.com/Comcast/webpa-common/wrp"
	"github.com/stretchr/testify/mock"
)

/*  Mocks for ConversionTool  */

type MockConversionTool struct {
	mock.Mock
}

func (m *MockConversionTool) GetFlavorFormat(req *http.Request, i string, i2 string, i3 string) (*GetWDMP, error) {
	args := m.Called(req, i, i2, i3)
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

/* Mocks for Logger */
