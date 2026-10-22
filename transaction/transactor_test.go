// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package transaction

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
)

func TestTransactError(t *testing.T) {
	assert := assert.New(t)

	plainErr := errors.New("network test error")
	expectedErr := NewXmidtUnavailableError(plainErr)

	transactor := New(&Options{
		Do: func(_ *http.Request) (*http.Response, error) {
			return nil, plainErr
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	_, e := transactor.Transact(r)

	assert.EqualValues(expectedErr, e)
	require.ErrorIs(t, e, plainErr, "the cause is kept for the log")
	require.ErrorIs(t, e, ErrXmidtUnavailable)
	assert.Equal(ErrXmidtUnavailable.Error(), CallerMessage(e), "the caller is not told the cause")
}

func TestTransactIdeal(t *testing.T) {
	assert := assert.New(t)

	expected := &XmidtResponse{
		Code:             404,
		Body:             []byte("not found"),
		ForwardedHeaders: http.Header{"X-A": []string{"a", "b"}},
	}

	rawXmidtResponse := &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(bytes.NewBufferString("not found")),
		Header: http.Header{
			"X-A": []string{"a", "b"}, // should be forwarded
			"Y-A": []string{"c", "d"}, // should be ignored
		},
	}

	transactor := New(&Options{
		Do: func(_ *http.Request) (*http.Response, error) {
			return rawXmidtResponse, nil
		},
	})

	r := httptest.NewRequest(http.MethodGet, "localhost:6003/test", nil)
	actual, e := transactor.Transact(r)
	require.NoError(t, e)
	assert.Equal(expected, actual)
}

func TestForwardHeadersByPrefix(t *testing.T) {
	t.Run("NoHeaders", func(t *testing.T) {
		assert := assert.New(t)

		var to, from = make(http.Header), make(http.Header)

		ForwardHeadersByPrefix("H", from, to)
		assert.Empty(to)
	})

	t.Run("MultipleHeadersFiltered", func(t *testing.T) {
		assert := assert.New(t)
		var to, from = make(http.Header), make(http.Header)

		from.Add("Helium", "3")
		from.Add("Hydrogen", "5")
		from.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("He", from, to)
		assert.NotEmpty(to)
		assert.Len(to, 1)
		assert.Equal("3", to.Get("Helium"))
	})

	t.Run("MultipleHeadersFilteredFullArray", func(t *testing.T) {
		assert := assert.New(t)

		var to, from = make(http.Header), make(http.Header)

		from.Add("Helium", "3")
		from.Add("Hydrogen", "5")
		from.Add("Hydrogen", "6")

		ForwardHeadersByPrefix("H", from, to)
		assert.NotEmpty(to)
		assert.Len(to, 2)
		assert.Equal([]string{"5", "6"}, to["Hydrogen"])
	})

	t.Run("NilCases", func(t *testing.T) {
		var to, from = make(http.Header), make(http.Header)
		// none of these should panic
		ForwardHeadersByPrefix("", nil, nil)
		ForwardHeadersByPrefix("", from, nil)
		ForwardHeadersByPrefix("", from, to)
	})
}

func TestWelcome(t *testing.T) {
	tests := []struct {
		description string
		genReq      func() *http.Request
		expectedTID string
	}{
		{
			description: "Generated TID",
			genReq: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost", nil)
				return
			},
		},
		{
			description: "Given TID",
			genReq: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost", nil)
				r.Header.Set(candlelight.HeaderWPATIDKeyName, "tid01")
				return
			},
			expectedTID: "tid01",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert := assert.New(t)
			handler := http.HandlerFunc(
				func(_ http.ResponseWriter, r *http.Request) {
					assert.NotNil(r.Context().Value(ContextKeyRequestArrivalTime))
					tid := r.Context().Value(ContextKeyRequestTID)
					if !assert.NotNil(tid) {
						return
					}
					tid = tid.(string)
					if assert.NotZero(tid) && tc.expectedTID != "" {
						assert.Equal(tc.expectedTID, tid)
					}
				})
			decorated := Welcome(FingerprintConfig{})(handler)
			decorated.ServeHTTP(nil, tc.genReq())

		})
	}
}

func TestLog(t *testing.T) {
	arrival := time.Now().Add(-time.Second)
	ctxWithArrivalTime := context.WithValue(context.Background(), ContextKeyRequestArrivalTime, arrival)
	ctxWithTID := context.WithValue(context.Background(), ContextKeyRequestTID, "tid-123")

	// a request that is being traced carries a valid span
	traceID, _ := trace.TraceIDFromHex("0af7651916cd43dd8448eb211c80319c")
	spanID, _ := trace.SpanIDFromHex("b7ad6b7169203331")
	ctxWithTrace := trace.ContextWithSpanContext(ctxWithTID, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID,
		SpanID:  spanID,
	}))

	tcs := []struct {
		desc                        string
		reducedLoggingResponseCodes []int
		ctx                         context.Context
		code                        int
		// wantError is whether the missing arrival time is reported, with
		// the tid when there is one and the trace and span IDs when the
		// request is traced.
		wantError   bool
		wantTID     string
		wantTraceID string
		wantSpanID  string
		// wantHeaders is whether the response line carries the headers.
		wantHeaders bool
	}{
		{
			desc:                        "arrival time present",
			reducedLoggingResponseCodes: []int{},
			ctx:                         ctxWithArrivalTime,
			code:                        200,
			wantHeaders:                 true,
		},
		{
			desc:                        "arrival time missing",
			reducedLoggingResponseCodes: []int{},
			ctx:                         context.Background(),
			code:                        200,
			wantError:                   true,
			wantHeaders:                 true,
		},
		{
			desc:                        "arrival time missing with a tid",
			reducedLoggingResponseCodes: []int{},
			ctx:                         ctxWithTID,
			code:                        200,
			wantError:                   true,
			wantTID:                     "tid-123",
			wantHeaders:                 true,
		},
		{
			desc:                        "arrival time missing on a traced request",
			reducedLoggingResponseCodes: []int{},
			ctx:                         ctxWithTrace,
			code:                        200,
			wantError:                   true,
			wantTID:                     "tid-123",
			wantTraceID:                 "0af7651916cd43dd8448eb211c80319c",
			wantSpanID:                  "b7ad6b7169203331",
			wantHeaders:                 true,
		},
		{
			desc:                        "reduced logging response code",
			reducedLoggingResponseCodes: []int{200},
			ctx:                         ctxWithArrivalTime,
			code:                        200,
			wantHeaders:                 false,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)

			core, logs := observer.New(zapcore.DebugLevel)
			ctx := context.WithValue(tc.ctx, ContextKeyResponseHeaders, http.Header{"X-Test": []string{"yes"}})
			ctx = sallust.With(ctx, zap.New(core))

			Log(tc.reducedLoggingResponseCodes)(ctx, tc.code, &http.Request{})

			entries := logs.All()
			if tc.wantError {
				require.Len(entries, 2, "the missing arrival time is reported before the response")
				assert.Equal(zapcore.ErrorLevel, entries[0].Level)
				assert.Equal("request arrival time not captured", entries[0].Message)

				fields := entries[0].ContextMap()
				assert.Equal(tc.wantTID, fields["tid"])
				if tc.wantTraceID == "" {
					assert.NotContains(fields, candlelight.TraceIdLogKeyName, "an untraced request has no trace ID to report")
					assert.NotContains(fields, candlelight.SpanIDLogKeyName)
				} else {
					assert.Equal(tc.wantTraceID, fields[candlelight.TraceIdLogKeyName])
					assert.Equal(tc.wantSpanID, fields[candlelight.SpanIDLogKeyName])
				}

				entries = entries[1:]
			}

			require.Len(entries, 1)
			line := entries[0]
			assert.Equal(zapcore.InfoLevel, line.Level)
			assert.Equal("response", line.Message)

			fields := line.ContextMap()
			if tc.wantError {
				assert.NotContains(fields, "duration", "no arrival time, no duration")
			} else {
				duration, ok := fields["duration"].(time.Duration)
				require.True(ok, "duration is a time.Duration")
				assert.GreaterOrEqual(duration, time.Second, "measured from the arrival time")
			}

			body, ok := fields["response"].(response)
			require.True(ok, "the response is logged as the response struct")
			assert.Equal(tc.code, body.Code)
			if tc.wantHeaders {
				assert.NotNil(body.Headers)
			} else {
				assert.Nil(body.Headers)
			}
		})
	}
}

func TestAddDeviceIdToLog(t *testing.T) {
	tests := []struct {
		desc     string
		ctx      context.Context
		req      func() (r *http.Request)
		deviceid string
	}{
		{
			desc: "device id in request",
			ctx:  context.Background(),
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445577"})
				return
			},
			deviceid: "mac:112233445577",
		},
		{
			desc: "device id added in code",
			ctx:  context.Background(),
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				return
			},
			deviceid: "mac:000000000000",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			observedZapCore, observedLogs := observer.New(zap.DebugLevel)
			observedLogger := zap.New(observedZapCore)
			ctx := sallust.With(tc.ctx, observedLogger)
			ctx = addDeviceIdToLog(ctx, tc.req())

			logger := sallust.Get(ctx)
			logger.Debug("test")
			gotLog := observedLogs.All()[0].Context

			assert.Equal("deviceid", gotLog[0].Key)
			assert.Equal(tc.deviceid, gotLog[0].String)

		})
	}
}

func TestBearerToken(t *testing.T) {
	tests := []struct {
		desc     string
		header   string
		expected string
	}{
		{desc: "no header", header: "", expected: ""},
		{desc: "basic auth ignored", header: "Basic dXNlcjpwYXNz", expected: ""},
		{desc: "bearer missing token", header: "Bearer", expected: ""},
		{desc: "happy path", header: "Bearer abc.def.ghi", expected: "abc.def.ghi"},
		{desc: "case-insensitive scheme", header: "bearer abc.def.ghi", expected: "abc.def.ghi"},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			assert.Equal(tc.expected, bearerToken(r))
		})
	}
}

func TestAddBearerFingerprintToLog(t *testing.T) {
	const token = "header.payload.abcdefghij1234567890"
	sum := sha256.Sum256([]byte(token))
	sha := hex.EncodeToString(sum[:])
	shortSum := sha256.Sum256([]byte("short"))
	shortSHA := hex.EncodeToString(shortSum[:])

	tests := []struct {
		desc     string
		cfg      FingerprintConfig
		header   string
		expected map[string]any // contents of the bearerFingerprint object; nil means no field at all
	}{
		{
			desc:     "default cfg logs nothing even with bearer token",
			cfg:      FingerprintConfig{},
			header:   "Bearer " + token,
			expected: nil,
		},
		{
			desc:     "no bearer header logs nothing",
			cfg:      FingerprintConfig{SHA256: true, LastNDigits: 10},
			header:   "",
			expected: nil,
		},
		{
			desc:     "suffix only",
			cfg:      FingerprintConfig{LastNDigits: 10},
			header:   "Bearer " + token,
			expected: map[string]any{"suffix": "1234567890"},
		},
		{
			desc:     "sha only",
			cfg:      FingerprintConfig{SHA256: true},
			header:   "Bearer " + token,
			expected: map[string]any{"sha": sha},
		},
		{
			desc:     "both",
			cfg:      FingerprintConfig{SHA256: true, LastNDigits: 10},
			header:   "Bearer " + token,
			expected: map[string]any{"suffix": "1234567890", "sha": sha},
		},
		{
			desc:     "suffix requested but token too short is skipped",
			cfg:      FingerprintConfig{LastNDigits: 10},
			header:   "Bearer short",
			expected: nil,
		},
		{
			desc:     "sha still logged when token is shorter than requested suffix",
			cfg:      FingerprintConfig{SHA256: true, LastNDigits: 10},
			header:   "Bearer short",
			expected: map[string]any{"sha": shortSHA},
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			core, observed := observer.New(zap.DebugLevel)
			ctx := sallust.With(context.Background(), zap.New(core))

			r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
			if tc.header != "" {
				r.Header.Set("Authorization", tc.header)
			}
			ctx = addBearerFingerprintToLog(ctx, r, tc.cfg)
			sallust.Get(ctx).Debug("test")

			ctxMap := observed.All()[0].ContextMap()
			if tc.expected == nil {
				_, present := ctxMap["bearerFingerprint"]
				assert.False(present, "bearerFingerprint should not be present")
				return
			}
			assert.Equal(tc.expected, ctxMap["bearerFingerprint"])
		})
	}
}

func TestGetDeviceId(t *testing.T) {
	tests := []struct {
		desc     string
		req      func() *http.Request
		expected string
	}{
		{
			desc: "Request has id",
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:11:22:33:44:55:Aa"})
				return
			},
			expected: "mac:1122334455aa",
		},
		{
			desc: "no id",
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				return
			},
			expected: "mac:000000000000",
		},
		{
			desc: "invalid id",
			req: func() (r *http.Request) {
				r = httptest.NewRequest(http.MethodGet, "http://localhost:6100/api/v2/device/", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "unsupported:frog"})
				return
			},
			expected: "invalid:unsupported:frog",
		},
	}
	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			assert := assert.New(t)
			id := getDeviceId(tc.req())
			assert.NotNil(id)
			assert.Equal(tc.expected, id)
		})
	}
}

// TestMarshalJSON marshals pointers to Request and response, alone and inside
// another value.  Their MarshalJSON methods once called json.Marshal on
// themselves, which recursed until the stack overflowed and the process died.
func TestMarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		v    any
		want string
	}{
		{
			name: "request",
			v:    &Request{Address: "198.51.100.7", Path: "/api/v3/device", Query: "names=x", Method: http.MethodGet},
			want: `{"address":"198.51.100.7","path":"/api/v3/device","query":"names=x","method":"GET"}`,
		}, {
			name: "empty request omits every field",
			v:    &Request{},
			want: `{}`,
		}, {
			name: "response",
			v:    &response{Code: http.StatusOK, Headers: map[string][]string{"X-Test": {"a"}}},
			want: `{"code":200,"headers":{"X-Test":["a"]}}`,
		}, {
			name: "empty response omits every field",
			v:    &response{},
			want: `{}`,
		}, {
			name: "nested",
			v: struct {
				Request  *Request  `json:"request"`
				Response *response `json:"response"`
			}{&Request{Method: http.MethodPost}, &response{Code: http.StatusNotFound}},
			want: `{"request":{"method":"POST"},"response":{"code":404}}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.v)
			require.NoError(t, err)
			assert.JSONEq(t, tc.want, string(got))
		})
	}
}
