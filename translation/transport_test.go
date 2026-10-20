// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwt"

	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/tr1d1um/transaction"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/wrp-go/v5"

	"github.com/xmidt-org/bascule"
)

const (
	allowedPartners  = "allowedPartners"
	allowedResources = "allowedResources"
)

// ctxTID is a context with a defined value for a TID
var ctxTID = context.WithValue(context.Background(), transaction.ContextKeyRequestTID, "test-tid")

func TestDecodeRequest(t *testing.T) {
	t.Run("PayloadFailure", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		_, e := decodeRequest(ctxTID, r)
		assert.EqualValues(ErrEmptyNames, e)
	})

	t.Run("WRPWrapFailure", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
		r = mux.SetURLVars(r, map[string]string{
			"deviceid": "mac:112233445566"})
		wrpMsg, e := decodeRequest(ctxTID, r)
		require.NoError(t, e)
		assert.NotEmpty(wrpMsg)
	})

	t.Run("Ideal", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
		r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
		wrpMsg, e := decodeRequest(ctxTID, r)
		require.NoError(t, e)
		assert.NotEmpty(wrpMsg)
	})

	t.Run("TraceHeaders", func(t *testing.T) {
		tests := []struct {
			name        string
			traceparent string
			tracestate  string
			want        []string
		}{
			{
				name: "none",
			}, {
				name:        "traceparent only",
				traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
				want: []string{
					"traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
					"tracestate: ",
				},
			}, {
				name:        "traceparent and tracestate",
				traceparent: "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
				tracestate:  "congo=t61rcWkgMzE",
				want: []string{
					"traceparent: 00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01",
					"tracestate: congo=t61rcWkgMzE",
				},
			}, {
				name:       "tracestate without traceparent is dropped",
				tracestate: "congo=t61rcWkgMzE",
			},
		}

		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
				r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})
				r.Header.Set(authHeaderKey, "Basic dXNlcjpwYXNz")
				if tc.traceparent != "" {
					r.Header.Set("traceparent", tc.traceparent)
				}
				if tc.tracestate != "" {
					r.Header.Set("tracestate", tc.tracestate)
				}

				decoded, err := decodeRequest(ctxTID, r)
				require.NoError(t, err)
				assert.Equal(t, tc.want, decoded.WRPMessage.Headers)
				assert.Equal(t, "Basic dXNlcjpwYXNz", decoded.AuthHeaderValue)
			})
		}
	})
}

func TestDecodeRequestPartnerIDs(t *testing.T) {
	tests := []struct {
		name                   string
		tokenType              string
		attrMap                map[string]any
		addPartnerIDsInHeaders bool
		expectedPartnerIDs     []string
	}{
		{
			name:               "Partners from JWT",
			tokenType:          "jwt",
			attrMap:            map[string]any{allowedPartners: []string{"partnerA", "partnerB"}},
			expectedPartnerIDs: []string{"partnerA", "partnerB"},
		},

		{
			name:                   "Partners from headers",
			tokenType:              "sss",
			attrMap:                map[string]any{},
			addPartnerIDsInHeaders: true,
			expectedPartnerIDs:     []string{"partner0", "partner1"},
		},

		{
			name:      "No Patner IDs",
			tokenType: "jwt",
			attrMap:   map[string]any{},
		},

		{
			name:      "Wrong type for partners from JWT",
			tokenType: "jwt",
			attrMap: map[string]any{
				"allowedResources": map[string]any{
					"allowedPartners": map[string]string{"partner-A": "first", "partner-B": "second"},
				}},
			addPartnerIDsInHeaders: true,
			expectedPartnerIDs:     []string{"partner0", "partner1"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert := assert.New(t)
			var ctx context.Context
			r := httptest.NewRequest(http.MethodGet, "http://localhost?names='deviceField'", nil)
			r = mux.SetURLVars(r, map[string]string{"deviceid": "mac:112233445566"})

			if test.addPartnerIDsInHeaders {
				r.Header.Set(partnerIDHeader, "partner0")
				r.Header.Add(partnerIDHeader, "partner1")
			}

			if test.tokenType == "" {
				ctx = ctxTID
			} else {
				ctx = enrichWithBasculeToken(context.Background(), true, test.attrMap)
			}

			wrpMsg, e := decodeRequest(ctx, r)
			require.NoError(t, e)
			assert.Equal(test.expectedPartnerIDs, wrpMsg.WRPMessage.PartnerIDs)
		})
	}
}

func TestRequestPayload(t *testing.T) {
	t.Run("Get", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrEmptyNames, e)
	})

	t.Run("Set", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPatch, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrInvalidSetWDMP, e)
	})

	t.Run("SetWithBody", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPatch, "http://localhost", bytes.NewBufferString("invalidWDMP"))
		_, e := requestPayload(r)
		var err transaction.CodedError
		require.ErrorAs(t, e, &err)
		assert.Contains(e.Error(), "invalid WDMP structure")
		assert.Equal(http.StatusBadRequest, err.StatusCode())

	})

	t.Run("Del", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodDelete, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("Replace", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPut, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("Add", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodPost, "http://localhost", nil)

		r = mux.SetURLVars(r, map[string]string{
			"service": "add"})
		_, e := requestPayload(r)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("Others", func(t *testing.T) {
		assert := assert.New(t)
		r := httptest.NewRequest(http.MethodOptions, "http://localhost", nil)
		_, e := requestPayload(r)
		assert.EqualValues(ErrUnsupportedMethod, e)
	})
}

func TestRequestGetPayload(t *testing.T) {
	t.Run("EmptyNames", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("", "")
		assert.EqualValues(ErrEmptyNames, e)
		assert.Nil(p)
	})

	t.Run("GET", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("n0,n1", "")
		require.NoError(t, e)

		expectedBytes, err := json.Marshal(&getWDMP{Command: CommandGet, Names: []string{"n0", "n1"}})

		if err != nil {
			panic(err)
		}

		assert.Equal(expectedBytes, p)
	})

	t.Run("NamesAreTrimmed", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload(" n0 ,, n1,", "")
		require.NoError(t, e)

		expectedBytes, err := json.Marshal(&getWDMP{Command: CommandGet, Names: []string{"n0", "n1"}})
		if err != nil {
			panic(err)
		}

		assert.Equal(expectedBytes, p)
	})

	t.Run("OnlyBlankNames", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload(" , ", "")
		assert.EqualValues(ErrEmptyNames, e)
		assert.Nil(p)
	})

	t.Run("GETAttrs", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestGetPayload("n0,n1", "attr0")
		require.NoError(t, e)

		expectedBytes, err := json.Marshal(&getWDMP{Command: CommandGetAttrs, Names: []string{"n0", "n1"}, Attributes: "attr0"})

		if err != nil {
			panic(err)
		}

		assert.Equal(expectedBytes, p)
	})
}

func TestRequestSetPayload(t *testing.T) {
	t.Run("ErrAtDeduction", func(t *testing.T) {
		assert := assert.New(t)
		_, e := requestSetPayload(bytes.NewBufferString(""), "", "old", "sync")

		assert.EqualValues(ErrNewCIDRequired, e)
	})

	t.Run("InvalidWDMP", func(t *testing.T) {
		assert := assert.New(t)
		_, e := requestSetPayload(bytes.NewBufferString(""), "", "", "")

		assert.EqualValues(ErrInvalidSetWDMP, e)
	})

	t.Run("Ideal", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestSetPayload(bytes.NewBufferString(""), "new", "old", "sync")

		wdmp := new(setWDMP)
		err := json.NewDecoder(bytes.NewBuffer(p)).Decode(wdmp)

		if err != nil {
			panic(err)
		}

		require.NoError(t, e)
		assert.Equal(CommandTestSet, wdmp.Command)
		assert.Equal("new", wdmp.NewCid)
		assert.Equal("old", wdmp.OldCid)
		assert.Equal("sync", wdmp.SyncCmc)
	})
}

func TestRequestAddPayload(t *testing.T) {
	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestAddPayload(nil, nil)
		assert.Nil(p)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("RowNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestAddPayload(map[string]string{
			"parameter": "t0"}, bytes.NewBufferString(""))

		assert.Nil(p)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("RowInvalidProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestAddPayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString("invalid row"))

		assert.Nil(p)
		assert.EqualValues(ErrInvalidRow, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestAddPayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(`{"row": "r0"}`))

		require.NoError(t, e)

		expected, err := json.Marshal(&addRowWDMP{
			Command: CommandAddRow,
			Table:   "t0",
			Row:     map[string]string{"row": "r0"},
		})

		if err != nil {
			panic(err)
		}

		assert.Equal(expected, p)
	})
}

func TestRequestReplacePayload(t *testing.T) {
	t.Run("TableNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestReplacePayload(nil, nil)
		assert.Nil(p)
		assert.EqualValues(ErrMissingTable, e)
	})

	t.Run("RowsNotProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestReplacePayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(""))

		assert.Nil(p)
		assert.EqualValues(ErrMissingRows, e)
	})

	t.Run("RowsInvalidProvided", func(t *testing.T) {
		assert := assert.New(t)

		p, e := requestReplacePayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString("invalid rows"))

		assert.Nil(p)
		assert.EqualValues(ErrInvalidRows, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)

		rowsPayload := `{"0": {"row": "r0"}}`

		p, e := requestReplacePayload(map[string]string{"parameter": "t0"}, bytes.NewBufferString(rowsPayload))

		require.NoError(t, e)

		expected, err := json.Marshal(&replaceRowsWDMP{
			Command: CommandReplaceRows,
			Table:   "t0",
			Rows:    indexRow{"0": map[string]string{"row": "r0"}},
		})

		if err != nil {
			panic(err)
		}

		assert.Equal(expected, p)
	})
}

func TestRequestDeletePayload(t *testing.T) {
	t.Run("NoRowProvided", func(t *testing.T) {
		assert := assert.New(t)
		p, e := requestDeletePayload(nil)

		assert.Nil(p)
		assert.EqualValues(ErrMissingRow, e)
	})

	t.Run("IdealPath", func(t *testing.T) {
		assert := assert.New(t)

		expected, err := json.Marshal(&deleteRowDMP{Command: CommandDeleteRow,
			Row: "0",
		})
		if err != nil {
			panic(err)
		}

		p, e := requestDeletePayload(map[string]string{"parameter": "0"})

		require.NoError(t, e)
		assert.Equal(expected, p)
	})
}

func TestEncodeResponse(t *testing.T) {
	assert := assert.New(t)

	// XMiDT response status code is not 200.
	// Tr1d1um should just forward such response code and body
	t.Run("StatusNotOK", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		response := &transaction.XmidtResponse{
			Code:             http.StatusServiceUnavailable,
			Body:             []byte("t"),
			ForwardedHeaders: http.Header{"X-test": []string{"test"}},
		}

		err := encodeResponse(ctxTID, recorder, response)

		require.NoError(t, err)
		assert.Equal(http.StatusServiceUnavailable, recorder.Code)
		assert.Equal("t", recorder.Body.String())
		assert.Equal("test", recorder.Header().Get("X-test"))
	})

	// XMiDT response is not msgpack-encoded
	// Since this is not expected, Tr1d1um considers it an internal error case
	t.Run("UnexpectedResponseFormat", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		response := &transaction.XmidtResponse{
			Code: http.StatusOK,
			Body: []byte("t"),
		}

		assert.Error(encodeResponse(ctxTID, recorder, response))
	})

	// XMiDt responds with a 200 (OK) with a well-formatted RDK device response
	// Tr1d1um returns the status provided by the device
	t.Run("RDKDeviceResponse", func(t *testing.T) {
		tests := []struct {
			resp               *transaction.XmidtResponse
			expectedStatusCode int
			deviceStatusCode   int
		}{
			{
				resp: &transaction.XmidtResponse{
					Code: http.StatusOK,
					Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
						Type:    wrp.SimpleRequestResponseMessageType,
						Payload: []byte(`{"statusCode": 520}`),
					}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
				},
				expectedStatusCode: 520,
				deviceStatusCode:   520,
			},
			{
				resp: &transaction.XmidtResponse{
					Code: http.StatusOK,
					Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
						Type:    wrp.SimpleRequestResponseMessageType,
						Payload: []byte(`{"statusCode": 599}`),
					}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
				},
				expectedStatusCode: 599,
				deviceStatusCode:   599,
			},
			{
				resp: &transaction.XmidtResponse{
					Code: http.StatusOK,
					Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
						Type:    wrp.SimpleRequestResponseMessageType,
						Payload: []byte(`{"statusCode": 1000}`),
					}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
				},
				expectedStatusCode: 200,
				deviceStatusCode:   1000,
			},
			{
				resp: &transaction.XmidtResponse{
					Code: http.StatusOK,
					Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
						Type:    wrp.SimpleRequestResponseMessageType,
						Payload: []byte(`{"statusCode": -1}`),
					}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
				},
				expectedStatusCode: 200,
				deviceStatusCode:   -1,
			},
		}
		for _, tc := range tests {
			t.Run(fmt.Sprintf("statusCode:%d", tc.expectedStatusCode), func(t *testing.T) {
				recorder := httptest.NewRecorder()
				err := encodeResponse(ctxTID, recorder, tc.resp)
				require.NoError(t, err)
				assert.Equal(tc.expectedStatusCode, recorder.Code)
				assert.Equal(fmt.Sprintf(`{"statusCode": %d}`, tc.deviceStatusCode), recorder.Body.String())
			})
		}
	})

	// RDK device is having an internal error and returns 500.
	// Tr1d1um, in order to avoid ambiguity, should not return 500.
	//Rationale: Tr1d1um is not the one having an internal error, it is the device.
	t.Run("InternalRDKDeviceError", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		internalErrorResponse := []byte(`{"statusCode": 500, "message": "I, the device, suffer"}`)

		response := &transaction.XmidtResponse{
			Code: http.StatusOK,
			Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: internalErrorResponse}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
		}

		err := encodeResponse(ctxTID, recorder, response)
		require.NoError(t, err)
		assert.Equal(http.StatusOK, recorder.Code)
		assert.Equal(internalErrorResponse, recorder.Body.Bytes())
	})

	// For whatever reason, the device may respond with incomplete or unexpected data
	// In such case, Tr1d1um just forwards as much as it could from the device
	t.Run("BadRDKDeviceResponse", func(t *testing.T) {
		recorder := httptest.NewRecorder()

		response := &transaction.XmidtResponse{
			Code: http.StatusOK,
			Body: bytes.NewBuffer(wrp.MustEncode(&wrp.Message{
				Type:    wrp.SimpleRequestResponseMessageType,
				Payload: []byte(`{"statusCode":`),
			}, wrp.Msgpack, wrp.NoStandardValidation())).Bytes(),
		}

		err := encodeResponse(ctxTID, recorder, response)
		require.NoError(t, err)
		assert.Equal(http.StatusOK, recorder.Code)
		assert.Equal(`{"statusCode":`, recorder.Body.String())
	})
}

func TestEncodeError(t *testing.T) {
	t.Run("BadRequests", func(t *testing.T) {
		assert := assert.New(t)

		for _, e := range []error{
			ErrEmptyNames,
			ErrInvalidService,
			ErrInvalidSetWDMP,
			ErrMissingRow,
			ErrMissingRows,
			ErrMissingTable,
			ErrNewCIDRequired,
		} {
			w := httptest.NewRecorder()

			expected := bytes.NewBufferString("")
			json.NewEncoder(expected).Encode(map[string]string{
				"message": e.Error()})

			encodeError(ctxTID, e, w)
			assert.Equal(expected.String(), w.Body.String())
			assert.Equal(http.StatusBadRequest, w.Code)
		}
	})

	t.Run("Timeouts", func(t *testing.T) {
		assert := assert.New(t)

		for _, e := range []error{
			transaction.NewCodedError(errors.New("some network error"), http.StatusServiceUnavailable),
			transaction.NewCodedError(errors.New("deadline exceeded"), http.StatusServiceUnavailable),
		} {
			w := httptest.NewRecorder()

			expected := bytes.NewBufferString("")
			json.NewEncoder(expected).Encode(map[string]string{
				"message": e.Error()})

			encodeError(ctxTID, e, w)
			assert.Equal(expected.String(), w.Body.String())
			assert.Equal(http.StatusServiceUnavailable, w.Code)
		}
	})

	t.Run("InternalError", func(t *testing.T) {
		assert := assert.New(t)

		w := httptest.NewRecorder()
		encodeError(ctxTID, errors.New("something internal went unexpecting wrong"), w)

		expected := bytes.NewBufferString("")
		json.NewEncoder(expected).Encode(map[string]string{
			"message": transaction.ErrTr1d1umInternal.Error()})

		assert.Equal(expected.String(), w.Body.String())
	})
}

type testJWT struct {
	jwt.Token
}

func (t testJWT) Principal() string {
	p, _ := t.Token.Subject()
	return p
}

func (t testJWT) Get(k string) (any, bool) {
	return t.Field(k)
}

func (t testJWT) Audience() []string {
	v, _ := t.Token.Audience()

	return v
}

func (t testJWT) Expiration() time.Time {
	v, _ := t.Token.Expiration()

	return v
}

func (t testJWT) IssuedAt() time.Time {
	v, _ := t.Token.IssuedAt()

	return v
}

func (t testJWT) Issuer() string {
	v, _ := t.Token.Issuer()

	return v
}

func (t testJWT) JwtID() string {
	v, _ := t.Token.JwtID()

	return v
}

func (t testJWT) NotBefore() time.Time {
	v, _ := t.Token.NotBefore()

	return v
}

func (t testJWT) Subject() string {
	v, _ := t.Token.Subject()

	return v
}

func (t testJWT) Capabilities() (caps []string) {
	if v, ok := t.Field(basculejwt.CapabilitiesKey); ok {
		caps, _ = bascule.GetCapabilities(v)
	}

	return
}

func enrichWithBasculeToken(ctx context.Context, isBearer bool, attrMap map[string]any) context.Context {
	if isBearer {
		token, err := jwt.NewBuilder().
			Claim(allowedResources, attrMap).
			Subject("client0").
			Issuer("https://example.com").
			Build()
		if err != nil {
			panic(fmt.Errorf("failed to build test JWT: %v", err))
		}

		return bascule.WithToken(ctx, &testJWT{token})
	}

	return bascule.WithToken(ctx, bascule.StubToken("client0"))
}

// testClaimsOnly is a JWT that exposes its claims but no attributes.
type testClaimsOnly struct {
	basculejwt.Claims
}

func (t testClaimsOnly) Principal() string { return t.Subject() }

func TestGetPartnerIDsDecodeRequest(t *testing.T) {
	jwtWith := func(attrs map[string]any) testJWT {
		token, err := jwt.NewBuilder().Claim(allowedResources, attrs).Subject("client0").Build()
		require.NoError(t, err)
		return testJWT{token}
	}

	headerIDs := []string{"partner0", "partner1", "partner2"}

	tests := []struct {
		name    string
		token   bascule.Token
		headers bool
		want    []string
	}{
		{
			name:    "no token uses the headers",
			headers: true,
			want:    headerIDs,
		}, {
			name: "no token and no headers",
		}, {
			name:    "a token that is not a jwt uses the headers",
			token:   bascule.StubToken("client0"),
			headers: true,
			want:    headerIDs,
		}, {
			name:    "a jwt without attributes uses the headers",
			token:   testClaimsOnly{jwtWith(map[string]any{allowedPartners: []string{"partnerA"}})},
			headers: true,
			want:    headerIDs,
		}, {
			name:    "a jwt without partner ids uses the headers",
			token:   jwtWith(map[string]any{}),
			headers: true,
			want:    headerIDs,
		}, {
			name:    "a jwt whose partner ids are not a list uses the headers",
			token:   jwtWith(map[string]any{allowedPartners: map[string]any{"partnerA": true}}),
			headers: true,
			want:    headerIDs,
		}, {
			name:    "a jwt's partner ids win over the headers",
			token:   jwtWith(map[string]any{allowedPartners: []string{"partnerA", "partnerB"}}),
			headers: true,
			want:    []string{"partnerA", "partnerB"},
		}, {
			name:  "a jwt's partner ids need no headers",
			token: jwtWith(map[string]any{allowedPartners: []string{"partnerA"}}),
			want:  []string{"partnerA"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "http://localhost", nil)
			if tc.headers {
				// repeated headers and comma separated lists are both read
				r.Header.Add(partnerIDHeader, "partner0")
				r.Header.Add(partnerIDHeader, "partner1, partner2")
			}

			ctx := context.Background()
			if tc.token != nil {
				ctx = bascule.WithToken(ctx, tc.token)
			}

			assert.Equal(t, tc.want, getPartnerIDsDecodeRequest(ctx, r))
		})
	}
}
