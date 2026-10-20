// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/gorilla/mux"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/auth"
	"github.com/xmidt-org/tr1d1um/paramfilter"
	transaction "github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v5"
)

const (
	paramWiFiSSID         = "Device.WiFi.SSID"
	paramSecurityPassword = "Device.Security.Password"
	valueMyNetwork        = "MyNetwork"
	valueSecret           = "secret"
)

func TestValidateAndDeduceSETCommand(t *testing.T) {

	t.Run("newCIDMissing", func(t *testing.T) {
		assert := assert.New(t)
		wdmp := new(setWDMP)
		err := deduceSET(wdmp, "", "old-cid", "sync-cm")
		assert.EqualValues(ErrNewCIDRequired, err)
	})

	t.Run("", func(t *testing.T) {
		assert := assert.New(t)
		wdmp := new(setWDMP)
		err := deduceSET(wdmp, "", "", "")
		require.NoError(t, err)
		assert.Equal(CommandSet, wdmp.Command)

	})

	t.Run("TestSetNilValues", func(t *testing.T) {
		assert := assert.New(t)
		wdmp := new(setWDMP)

		err := deduceSET(wdmp, "newVal", "oldVal", "")
		require.NoError(t, err)
		assert.Equal(CommandTestSet, wdmp.Command)
	})
}

func TestIsValidSetWDMP(t *testing.T) {
	t.Run("TestAndSetZeroParams", func(t *testing.T) {
		assert := assert.New(t)

		wdmp := &setWDMP{Command: CommandTestSet} // nil parameters
		assert.True(isValidSetWDMP(wdmp))

		wdmp = &setWDMP{Command: CommandTestSet, Parameters: []setParam{}} // empty parameters
		assert.True(isValidSetWDMP(wdmp))
	})

	t.Run("NilNameInParam", func(t *testing.T) {
		assert := assert.New(t)

		dataType := int8(0)
		nilNameParam := setParam{
			Value:    "val",
			DataType: &dataType,
			// Name is left undefined
		}
		params := []setParam{nilNameParam}
		wdmp := &setWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("NilDataTypeNonNilValue", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := setParam{
			Name:  &name,
			Value: 3,
			// DataType is left undefined
		}
		params := []setParam{param}
		wdmp := &setWDMP{Command: CommandSet, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("SetAttrsParamNilAttr", func(t *testing.T) {
		assert := assert.New(t)

		name := "nameVal"
		param := setParam{
			Name: &name,
		}
		params := []setParam{param}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("MixedParams", func(t *testing.T) {
		assert := assert.New(t)

		name, dataType := "victorious", int8(1)
		setAttrParam := setParam{
			Name:       &name,
			Attributes: map[string]any{"three": 3},
		}

		sp := setParam{
			Name:       &name,
			Attributes: map[string]any{"two": 2},
			Value:      3,
			DataType:   &dataType,
		}
		mixParams := []setParam{setAttrParam, sp}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: mixParams}
		assert.False(isValidSetWDMP(wdmp))
	})

	t.Run("IdealSet", func(t *testing.T) {
		assert := assert.New(t)

		name := "victorious"
		setAttrParam := setParam{
			Name:       &name,
			Attributes: map[string]any{"three": 3},
		}
		params := []setParam{setAttrParam}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.True(isValidSetWDMP(wdmp))
	})
}

func TestGetCommandForParam(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		assert := assert.New(t)
		assert.Equal(CommandSet, getCommandForParams(nil))
		assert.Equal(CommandSet, getCommandForParams([]setParam{}))
	})

	// Attributes and Name are required properties for SET_ATTRS
	t.Run("SetCommandUndefinedAttributes", func(t *testing.T) {
		assert := assert.New(t)
		name := "setParam"
		setCommandParam := setParam{Name: &name}
		assert.Equal(CommandSet, getCommandForParams([]setParam{setCommandParam}))
	})

	// DataType and Value must be null for SET_ATTRS
	t.Run("SetAttrsCommand", func(t *testing.T) {
		assert := assert.New(t)
		name := "setAttrsParam"
		setCommandParam := setParam{
			Name:       &name,
			Attributes: map[string]any{"zero": 0},
		}
		assert.Equal(CommandSetAttrs, getCommandForParams([]setParam{setCommandParam}))
	})
}
func TestWrapInWRP(t *testing.T) {
	t.Run("EmptyVars", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte(""), "", nil, nil, nil)

		assert.Nil(w)
		require.ErrorIs(t, e, wrp.ErrorInvalidDeviceName)

		var coded transaction.CodedError
		require.ErrorAs(t, e, &coded)
		assert.Equal(http.StatusBadRequest, coded.StatusCode())
	})

	t.Run("GivenParameters", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte{'t'}, "t0", map[string]string{"deviceid": "mac:112233445566", "service": "s0"}, nil, nil)

		require.NoError(t, e)
		assert.Equal(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.Equal([]byte{'t'}, w.Payload)
		assert.Equal("mac:112233445566/s0", w.Destination)
		assert.Equal("t0", w.TransactionUUID)
	})
}

func TestDecodeValidServiceRequest(t *testing.T) {
	f := decodeValidServiceRequest([]string{"s0"}, func(_ context.Context, _ *http.Request) (*wrpRequest, error) {
		return nil, nil
	})

	t.Run("InvalidService", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "localhost:8090/api", nil)
		i, err := f(context.TODO(), r)
		assert.Nil(i)
		assert.EqualValues(ErrInvalidService, err)
	})

	t.Run("ValidService", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "localhost:8090/api", nil)
		r = mux.SetURLVars(r, map[string]string{"service": "s0"})

		i, err := f(context.TODO(), r)
		assert.Nil(i)
		assert.NoError(err)
	})
}

func TestContains(t *testing.T) {
	assert := assert.New(t)
	assert.False(contains("a", nil))
	assert.False(contains("a", []string{}))
	assert.True(contains("a", []string{"a", "b"}))
}

func TestGetParamNames(t *testing.T) {
	j := "Josh"
	b := "Brian"
	tcs := []struct {
		desc               string
		params             []setParam
		expectedParamnames []string
	}{
		{
			desc:               "Empty Params",
			params:             []setParam{},
			expectedParamnames: []string{},
		},
		{
			desc:               "Pull Params",
			params:             []setParam{{Name: &j}, {Name: &b}},
			expectedParamnames: []string{"Josh", "Brian"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			r := getParamNames(tc.params)
			assert.Equal(tc.expectedParamnames, r)
		})
	}
}

func TestFilterParamValues(t *testing.T) {
	name1 := paramWiFiSSID
	name2 := paramSecurityPassword
	dt := int8(0)
	params := []setParam{
		{Name: &name1, DataType: &dt, Value: valueMyNetwork},
		{Name: &name2, DataType: &dt, Value: valueSecret},
	}

	tests := []struct {
		name   string
		filter *paramfilter.Filter
		want   map[string]any
	}{
		{
			name:   "nil filter returns nil",
			filter: nil,
			want:   nil,
		},
		{
			name: "never mode no exceptions",
			filter: paramfilter.New(paramfilter.FilterConfig{
				Mode: paramfilter.ModeNever,
			}),
			want: nil,
		},
		{
			name: "never mode with matching exception",
			filter: paramfilter.New(paramfilter.FilterConfig{
				Mode:   paramfilter.ModeNever,
				Except: []string{"Device.WiFi.**"},
			}),
			want: map[string]any{paramWiFiSSID: valueMyNetwork},
		},
		{
			name: "always mode excludes match",
			filter: paramfilter.New(paramfilter.FilterConfig{
				Mode:   paramfilter.ModeAlways,
				Except: []string{"Device.Security.**"},
			}),
			want: map[string]any{paramWiFiSSID: valueMyNetwork},
		},
		{
			name: "always mode no exceptions returns all",
			filter: paramfilter.New(paramfilter.FilterConfig{
				Mode: paramfilter.ModeAlways,
			}),
			want: map[string]any{
				paramWiFiSSID:         valueMyNetwork,
				paramSecurityPassword: valueSecret,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := filterParamValues(params, tt.filter)
			assert.Equal(t, tt.want, got)
		})
	}
}

// newObservedContext creates a context with an observable logger for test assertions.
func newObservedContext() (context.Context, *observer.ObservedLogs) {
	core, logs := observer.New(zap.DebugLevel)
	logger := zap.New(core)
	return sallust.With(context.Background(), logger), logs
}

// extractStringSlice extracts the string slice from a zap.Strings field.
func extractStringSlice(fields []zapcore.Field, key string) []string {
	for _, f := range fields {
		if f.Key == key {
			enc := zapcore.NewMapObjectEncoder()
			f.AddTo(enc)
			if arr, ok := enc.Fields[key]; ok {
				if sl, ok := arr.([]any); ok {
					out := make([]string, len(sl))
					for i, v := range sl {
						out[i] = v.(string)
					}
					return out
				}
			}
		}
	}
	return nil
}

// hasField reports whether any of the fields has the given key.
func hasField(fields []zapcore.Field, key string) bool {
	for _, f := range fields {
		if f.Key == key {
			return true
		}
	}
	return false
}

func TestCaptureWDMPParameters_GET(t *testing.T) {
	tests := []struct {
		name           string
		filters        *paramfilter.Filters
		queryNames     string
		expectParams   bool
		expectedParams []string
	}{
		{
			name:         "empty names query does not log",
			filters:      paramfilter.NewFilters(paramfilter.Config{}),
			queryNames:   "",
			expectParams: false,
		},
		{
			name:           "always logs all names regardless of filter",
			filters:        paramfilter.NewFilters(paramfilter.Config{}),
			queryNames:     "Device.WiFi.SSID,Device.Ethernet.Status",
			expectParams:   true,
			expectedParams: []string{paramWiFiSSID, "Device.Ethernet.Status"},
		},
		{
			name:           "nil filters still logs names",
			filters:        nil,
			queryNames:     paramWiFiSSID,
			expectParams:   true,
			expectedParams: []string{paramWiFiSSID},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, logs := newObservedContext()
			r := httptest.NewRequest(http.MethodGet,
				"http://localhost?names="+tt.queryNames, nil)

			fn := captureWDMPParameters(tt.filters)
			newCtx := fn(ctx, r)

			sallust.Get(newCtx).Info("test")
			entries := logs.All()
			last := entries[len(entries)-1]

			if tt.expectParams {
				foundParams := extractStringSlice(last.Context, "parameters")
				assert.Equal(t, tt.expectedParams, foundParams)
			} else {
				assert.Nil(t, extractStringSlice(last.Context, "parameters"))
			}
		})
	}
}

func TestCaptureWDMPParameters_PATCH(t *testing.T) {
	name1 := paramWiFiSSID
	name2 := paramSecurityPassword
	dt := int8(0)
	body := setWDMP{
		Parameters: []setParam{
			{Name: &name1, DataType: &dt, Value: valueMyNetwork},
			{Name: &name2, DataType: &dt, Value: valueSecret},
		},
	}
	bodyBytes, _ := json.Marshal(body)

	allNames := []string{paramWiFiSSID, paramSecurityPassword}

	tests := []struct {
		name         string
		filters      *paramfilter.Filters
		expectNames  []string
		expectValues bool
	}{
		{
			name:        "nil filters logs names but no values",
			filters:     nil,
			expectNames: allNames,
		},
		{
			name: "never mode logs names but no values",
			filters: paramfilter.NewFilters(paramfilter.Config{
				Patch: paramfilter.FilterConfig{Mode: paramfilter.ModeNever},
			}),
			expectNames: allNames,
		},
		{
			name: "never mode with exception logs names and matched values",
			filters: paramfilter.NewFilters(paramfilter.Config{
				Patch: paramfilter.FilterConfig{
					Mode:   paramfilter.ModeNever,
					Except: []string{"Device.WiFi.**"},
				},
			}),
			expectNames:  allNames,
			expectValues: true,
		},
		{
			name: "always mode logs names and all values",
			filters: paramfilter.NewFilters(paramfilter.Config{
				Patch: paramfilter.FilterConfig{Mode: paramfilter.ModeAlways},
			}),
			expectNames:  allNames,
			expectValues: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, logs := newObservedContext()
			r := httptest.NewRequest(http.MethodPatch,
				"http://localhost", bytes.NewBuffer(bodyBytes))

			fn := captureWDMPParameters(tt.filters)
			newCtx := fn(ctx, r)

			sallust.Get(newCtx).Info("test")
			entries := logs.All()
			last := entries[len(entries)-1]

			// Names should always be present.
			foundParams := extractStringSlice(last.Context, "parameters")
			assert.Equal(t, tt.expectNames, foundParams)

			// Values should only be present when the filter allows them.
			assert.Equal(t, tt.expectValues, hasField(last.Context, "parameterValues"))
		})
	}
}

func TestExtractParameters(t *testing.T) {
	const (
		configPath   = "/api/v3/device/mac:112233445566/config"
		parameterVar = "parameter" // the mux variable naming a table or row
		modelName    = "Device.DeviceInfo.ModelName"
	)

	const body = `{"parameters":[{"name":"Device.WiFi.SSID.1.SSID","value":"home","dataType":0},{"name":"Device.WiFi.Radio.1.Channel","value":6,"dataType":1}]}`

	cases := []struct {
		name     string
		method   string
		target   string
		vars     map[string]string
		body     string
		wantOK   bool
		wantReq  auth.ParamRequest
		wantBody string // what the handler can still read afterwards
	}{
		{
			name:    "get names",
			method:  http.MethodGet,
			target:  configPath + "?names=" + modelName + ",Device.DeviceInfo.",
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationRead, Names: []string{modelName, "Device.DeviceInfo."}},
		}, {
			name:    "get names are trimmed and blanks dropped",
			method:  http.MethodGet,
			target:  configPath + "?names=" + url.QueryEscape(" "+modelName+" , ,Device.DeviceInfo. "),
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationRead, Names: []string{modelName, "Device.DeviceInfo."}},
		}, {
			name:   "get with only blank names is left to the handler",
			method: http.MethodGet,
			target: configPath + "?names=" + url.QueryEscape(" , "),
		}, {
			name:    "get attributes still reads the names",
			method:  http.MethodGet,
			target:  configPath + "?names=" + modelName + "&attributes=notify",
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationRead, Names: []string{modelName}},
		}, {
			name:   "get without names is left to the handler",
			method: http.MethodGet,
			target: configPath,
		}, {
			name:     "patch writes the parameters in its body, which the handler can still read",
			method:   http.MethodPatch,
			target:   configPath,
			body:     body,
			wantOK:   true,
			wantReq:  auth.ParamRequest{Operation: auth.OperationWrite, Names: []string{"Device.WiFi.SSID.1.SSID", "Device.WiFi.Radio.1.Channel"}},
			wantBody: body,
		}, {
			name:     "patch with an unreadable body is left to the handler, body intact",
			method:   http.MethodPatch,
			target:   configPath,
			body:     "not json",
			wantBody: "not json",
		}, {
			name:    "post writes the table as a partial path",
			method:  http.MethodPost,
			target:  configPath + "/Device.NAT.PortMapping",
			vars:    map[string]string{parameterVar: "Device.NAT.PortMapping"},
			body:    `{"InternalPort":"80"}`,
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationWrite, Names: []string{"Device.NAT.PortMapping."}},
		}, {
			name:    "put keeps a trailing dot and trims spaces",
			method:  http.MethodPut,
			target:  configPath + "/Device.NAT.PortMapping.",
			vars:    map[string]string{parameterVar: " Device.NAT.PortMapping. "},
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationWrite, Names: []string{"Device.NAT.PortMapping."}},
		}, {
			name:    "delete writes the row as a partial path",
			method:  http.MethodDelete,
			target:  configPath + "/Device.NAT.PortMapping.3",
			vars:    map[string]string{parameterVar: "Device.NAT.PortMapping.3"},
			wantOK:  true,
			wantReq: auth.ParamRequest{Operation: auth.OperationWrite, Names: []string{"Device.NAT.PortMapping.3."}},
		}, {
			name:   "a table operation without a path is left to the handler",
			method: http.MethodDelete,
			target: configPath + "/",
			vars:   map[string]string{parameterVar: ""},
		}, {
			name:   "another method names nothing",
			method: http.MethodHead,
			target: configPath + "?names=" + modelName,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var body io.Reader
			if tc.body != "" {
				body = strings.NewReader(tc.body)
			}

			r := httptest.NewRequest(tc.method, tc.target, body)
			if tc.vars != nil {
				r = mux.SetURLVars(r, tc.vars)
			}

			got, ok := extractParameters(r)
			assert.Equal(t, tc.wantOK, ok)
			if tc.wantOK {
				assert.Equal(t, tc.wantReq, got)
			}

			if tc.wantBody != "" {
				rest, err := io.ReadAll(r.Body)
				require.NoError(t, err)
				assert.Equal(t, tc.wantBody, string(rest))
			}
		})
	}
}
