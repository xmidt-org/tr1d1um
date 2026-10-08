// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"

	"github.com/stretchr/testify/assert"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/tr1d1um/paramfilter"
	transaction "github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v5"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
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
		assert.Nil(err)
		assert.EqualValues(CommandSet, wdmp.Command)

	})

	t.Run("TestSetNilValues", func(t *testing.T) {
		assert := assert.New(t)
		wdmp := new(setWDMP)

		err := deduceSET(wdmp, "newVal", "oldVal", "")
		assert.Nil(err)
		assert.EqualValues(CommandTestSet, wdmp.Command)
	})
}

func TestIsValidSetWDMP(t *testing.T) {
	t.Run("TestAndSetZeroParams", func(t *testing.T) {
		assert := assert.New(t)

		wdmp := &setWDMP{Command: CommandTestSet} //nil parameters
		assert.True(isValidSetWDMP(wdmp))

		wdmp = &setWDMP{Command: CommandTestSet, Parameters: []setParam{}} //empty parameters
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
			//DataType is left undefined
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
			Attributes: map[string]interface{}{"three": 3},
		}

		sp := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"two": 2},
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
			Attributes: map[string]interface{}{"three": 3},
		}
		params := []setParam{setAttrParam}
		wdmp := &setWDMP{Command: CommandSetAttrs, Parameters: params}
		assert.True(isValidSetWDMP(wdmp))
	})
}

func TestGetCommandForParam(t *testing.T) {
	t.Run("EmptyParams", func(t *testing.T) {
		assert := assert.New(t)
		assert.EqualValues(CommandSet, getCommandForParams(nil))
		assert.EqualValues(CommandSet, getCommandForParams([]setParam{}))
	})

	//Attributes and Name are required properties for SET_ATTRS
	t.Run("SetCommandUndefinedAttributes", func(t *testing.T) {
		assert := assert.New(t)
		name := "setParam"
		setCommandParam := setParam{Name: &name}
		assert.EqualValues(CommandSet, getCommandForParams([]setParam{setCommandParam}))
	})

	//DataType and Value must be null for SET_ATTRS
	t.Run("SetAttrsCommand", func(t *testing.T) {
		assert := assert.New(t)
		name := "setAttrsParam"
		setCommandParam := setParam{
			Name:       &name,
			Attributes: map[string]interface{}{"zero": 0},
		}
		assert.EqualValues(CommandSetAttrs, getCommandForParams([]setParam{setCommandParam}))
	})
}
func TestWrapInWRP(t *testing.T) {
	t.Run("EmptyVars", func(t *testing.T) {
		assert := assert.New(t)

		w, e := wrap([]byte(""), "", nil, nil, nil)

		assert.Nil(w)
		assert.ErrorIs(e, wrp.ErrorInvalidDeviceName)

		var coded transaction.CodedError
		assert.ErrorAs(e, &coded)
		assert.Equal(http.StatusBadRequest, coded.StatusCode())
	})

	t.Run("GivenParameters", func(t *testing.T) {
		assert := assert.New(t)

		// nolint: goconst
		w, e := wrap([]byte{'t'}, "t0", map[string]string{"deviceid": "mac:112233445566", "service": "s0"}, nil, nil)

		assert.Nil(e)
		assert.EqualValues(wrp.SimpleRequestResponseMessageType, w.Type)
		assert.EqualValues([]byte{'t'}, w.Payload)
		assert.EqualValues("mac:112233445566/s0", w.Destination)
		assert.EqualValues("t0", w.TransactionUUID)
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
		// nolint: goconst
		r = mux.SetURLVars(r, map[string]string{"service": "s0"})

		i, err := f(context.TODO(), r)
		assert.Nil(i)
		assert.Nil(err)
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
			assert.Equal(r, tc.expectedParamnames)
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
