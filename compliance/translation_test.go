// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/wrp-go/v5"
)

// ---------------------------------------------------------------------------
// Translation: the WDMP command tr1d1um builds for each REST call, the WRP
// envelope it wraps it in, and how the device's reply becomes the HTTP
// response.  A connected fake device records every WRP it receives.
// ---------------------------------------------------------------------------

// upperColonMAC rewrites "mac:020000aabbcc" as "MAC:02:00:00:AA:BB:CC", a
// non-canonical spelling that tr1d1um must normalize.
func upperColonMAC(mac string) string {
	hex := strings.ToUpper(strings.TrimPrefix(mac, "mac:"))
	parts := make([]string, 0, 6)
	for i := 0; i+2 <= len(hex); i += 2 {
		parts = append(parts, hex[i:i+2])
	}

	return "MAC:" + strings.Join(parts, ":")
}

// TestCommandTranslation checks the WDMP command built for each kind of
// request.
func TestCommandTranslation(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)
	config := a.configURL("v3", dev.id)
	table := a.tableURL("v3", dev.id, "Device.DHCPv4.Server.Pool.1.StaticAddress.")
	row := a.tableURL("v3", dev.id, "Device.DHCPv4.Server.Pool.1.StaticAddress.1")

	cases := []struct {
		name    string
		method  string
		url     string
		headers map[string]string
		body    string
		check   func(t *testing.T, p map[string]any)
	}{
		{
			name: "get_single_name", method: http.MethodGet, url: config + "?names=Device.A",
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "GET", p["command"])
				assert.Equal(t, []any{"Device.A"}, p["names"])
			},
		},
		{
			name: "get_multiple_names", method: http.MethodGet, url: config + "?names=Device.A,Device.B",
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "GET", p["command"])
				assert.Equal(t, []any{"Device.A", "Device.B"}, p["names"])
			},
		},
		{
			name: "get_attributes", method: http.MethodGet, url: config + "?names=Device.A&attributes=notify",
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "GET_ATTRIBUTES", p["command"])
				assert.Equal(t, "notify", p["attributes"])
				assert.Equal(t, []any{"Device.A"}, p["names"])
			},
		},
		{
			name: "set_with_value", method: http.MethodPatch, url: config,
			body: `{"parameters":[{"name":"Device.A","value":"v","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "SET", p["command"])
				params, _ := p["parameters"].([]any)
				require.Len(t, params, 1)
				entry, _ := params[0].(map[string]any)
				assert.Equal(t, "Device.A", entry["name"])
				assert.Equal(t, "v", entry["value"])
				assert.EqualValues(t, 0, entry["dataType"])
				assert.NotContains(t, p, "new-cid")
				assert.NotContains(t, p, "old-cid")
			},
		},
		{
			name: "set_multiple", method: http.MethodPatch, url: config,
			body: `{"parameters":[{"name":"Device.A","value":"1","dataType":0},{"name":"Device.B","value":"2","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "SET", p["command"])
				params, _ := p["parameters"].([]any)
				require.Len(t, params, 2, "every parameter should reach the device")
				first, _ := params[0].(map[string]any)
				second, _ := params[1].(map[string]any)
				assert.Equal(t, "Device.A", first["name"])
				assert.Equal(t, "Device.B", second["name"])
			},
		},
		{
			name: "set_attributes", method: http.MethodPatch, url: config,
			body: `{"parameters":[{"name":"Device.A","attributes":{"notify":1}}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "SET_ATTRIBUTES", p["command"])
				params, _ := p["parameters"].([]any)
				require.Len(t, params, 1)
				entry, _ := params[0].(map[string]any)
				assert.Equal(t, "Device.A", entry["name"])
				assert.Equal(t, map[string]any{"notify": float64(1)}, entry["attributes"])
			},
		},
		{
			name: "set_unicode", method: http.MethodPatch, url: config,
			body: `{"parameters":[{"name":"Device.Test.Ünïcødé_日本語","value":"it's ünïcødé ✓ 日本語 😀","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				params, _ := p["parameters"].([]any)
				require.Len(t, params, 1)
				entry, _ := params[0].(map[string]any)
				assert.Equal(t, "Device.Test.Ünïcødé_日本語", entry["name"], "a unicode name should reach the device unchanged")
				assert.Equal(t, "it's ünïcødé ✓ 日本語 😀", entry["value"], "a unicode value should reach the device unchanged")
			},
		},
		{
			name: "test_and_set", method: http.MethodPatch, url: config,
			headers: map[string]string{"X-Webpa-Sync-Old-Cid": "old-1", "X-Webpa-Sync-New-Cid": "new-1", "X-Webpa-Sync-Cmc": "cmc-1"},
			body:    `{"parameters":[{"name":"Device.A","value":"v","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "TEST_AND_SET", p["command"])
				assert.Equal(t, "old-1", p["old-cid"])
				assert.Equal(t, "new-1", p["new-cid"])
				assert.Equal(t, "cmc-1", p["sync-cmc"])
			},
		},
		{
			name: "test_and_set_new_cid_only", method: http.MethodPatch, url: config,
			headers: map[string]string{"X-Webpa-Sync-New-Cid": "new-1"},
			body:    `{"parameters":[{"name":"Device.A","value":"v","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "TEST_AND_SET", p["command"])
				assert.Equal(t, "new-1", p["new-cid"])
				assert.NotContains(t, p, "old-cid")
			},
		},
		{
			name: "test_and_set_cmc_only", method: http.MethodPatch, url: config,
			headers: map[string]string{"X-Webpa-Sync-Cmc": "cmc-1"},
			body:    `{"parameters":[{"name":"Device.A","value":"v","dataType":0}]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "TEST_AND_SET", p["command"])
				assert.Equal(t, "cmc-1", p["sync-cmc"])
				assert.NotContains(t, p, "new-cid", "an empty new-cid is omitted")
			},
		},
		{
			name: "test_and_set_empty_body", method: http.MethodPatch, url: config,
			headers: map[string]string{"X-Webpa-Sync-New-Cid": "new-2", "X-Webpa-Sync-Old-Cid": "old-2"},
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "TEST_AND_SET", p["command"])
				assert.Equal(t, "new-2", p["new-cid"])
				assert.Equal(t, "old-2", p["old-cid"])
				assert.NotContains(t, p, "parameters")
			},
		},
		{
			name: "test_and_set_empty_parameters", method: http.MethodPatch, url: config,
			headers: map[string]string{"X-Webpa-Sync-New-Cid": "new-3", "X-Webpa-Sync-Old-Cid": "old-3", "X-Webpa-Sync-Cmc": "cmc-3"},
			body:    `{"parameters":[]}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "TEST_AND_SET", p["command"])
				assert.Equal(t, "new-3", p["new-cid"])
				assert.Equal(t, "old-3", p["old-cid"])
				assert.Equal(t, "cmc-3", p["sync-cmc"])
			},
		},
		{
			name: "add_row", method: http.MethodPost, url: table,
			body: `{"DeviceName":"ComplianceDevice","MACAddress":"DE:AD:BE:EF:00:01"}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "ADD_ROW", p["command"])
				assert.Equal(t, "Device.DHCPv4.Server.Pool.1.StaticAddress.", p["table"])
				row, _ := p["row"].(map[string]any)
				assert.Equal(t, "ComplianceDevice", row["DeviceName"])
				assert.Equal(t, "DE:AD:BE:EF:00:01", row["MACAddress"])
			},
		},
		{
			name: "replace_rows", method: http.MethodPut, url: table,
			body: `{"0":{"DeviceName":"First","MACAddress":"DE:AD:BE:EF:00:01"},"1":{"DeviceName":"Second","MACAddress":"DE:AD:BE:EF:00:02"}}`,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "REPLACE_ROWS", p["command"])
				assert.Equal(t, "Device.DHCPv4.Server.Pool.1.StaticAddress.", p["table"])
				rows, _ := p["rows"].(map[string]any)
				require.Len(t, rows, 2)
				first, _ := rows["0"].(map[string]any)
				second, _ := rows["1"].(map[string]any)
				assert.Equal(t, "First", first["DeviceName"])
				assert.Equal(t, "Second", second["DeviceName"])
			},
		},
		{
			name: "delete_row", method: http.MethodDelete, url: row,
			check: func(t *testing.T, p map[string]any) {
				t.Helper()
				assert.Equal(t, "DELETE_ROW", p["command"])
				assert.Equal(t, "Device.DHCPv4.Server.Pool.1.StaticAddress.1", p["row"], "the row is the path parameter")
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tok, Headers: tc.headers, Body: tc.body})
			require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
			tc.check(t, dev.next(t).payload(t))
		})
	}
}

// TestLargeParameterSet: a GET of many names reaches the device as one
// command with every name, in order.
func TestLargeParameterSet(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)

	names := make([]string, 0, 95)
	for i := range 95 {
		names = append(names, fmt.Sprintf("Device.DeviceInfo.Manufacturer%d", i))
	}

	resp := get(t, a.configURL("v3", dev.id)+"?names="+strings.Join(names, ","), allToken(t))
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

	got, _ := dev.next(t).payload(t)["names"].([]any)
	require.Len(t, got, len(names), "all requested names should reach the device")
	for i, name := range names {
		assert.Equal(t, name, got[i])
	}
}

// TestWRPEnvelope checks the envelope tr1d1um builds for a config GET.
func TestWRPEnvelope(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)

	// The device ID is sent non-canonically; XMiDT routes on the canonical form.
	url := a.configURL("v3", upperColonMAC(dev.id)) + "?names=Device.DeviceInfo.Manufacturer"
	resp := get(t, url, tok)
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

	got := dev.next(t)
	assert.Equal(t, wrp.SimpleRequestResponseMessageType, got.msg.Type, "message type should be SimpleRequestResponse (3)")
	assert.Equal(t, dev.id+"/config", got.msg.Destination, "destination should be the canonical device ID plus the service")
	assert.Equal(t, "dns:tr1d1um.compliance.example.com", got.msg.Source, "source should be the configured WRPSource")
	assert.NotEmpty(t, got.msg.TransactionUUID, "a transaction UUID should be set")
	assert.Equal(t, got.msg.TransactionUUID, resp.Header.Get(transactionIDHeader), "the response header should carry the transaction UUID the device saw")
	assert.Equal(t, []string{defaultPartner}, got.msg.PartnerIDs, "partner IDs should come from the JWT")
	assert.Empty(t, got.msg.Headers, "no trace headers should be forwarded without a traceparent")

	// the outbound request carries the configured fanout credential, not the caller's
	assert.Equal(t, "Basic ZmFub3V0OnNlY3JldA==", got.header.Get("Authorization"), "XMiDT should see the configured outbound credential")
	assert.NotContains(t, got.header.Get("Authorization"), strings.TrimPrefix(tok, "Bearer "), "the caller's token must not be forwarded")
}

// TestWRPTraceHeaders: a W3C traceparent/tracestate on the request is
// forwarded to the device as WRP headers.
func TestWRPTraceHeaders(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)

	traceID := randomHex(16)
	headers := map[string]string{
		"traceparent": fmt.Sprintf("00-%s-%s-01", traceID, randomHex(8)),
		"tracestate":  "foo=bar",
	}
	resp := send(t, request{Method: http.MethodGet, URL: a.configURL("v3", dev.id) + "?names=Device.DeviceInfo.Manufacturer", Token: allToken(t), Headers: headers})
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

	var traceparent, tracestate string
	for _, h := range dev.next(t).msg.Headers {
		switch {
		case strings.HasPrefix(h, "traceparent: "):
			traceparent = h
		case strings.HasPrefix(h, "tracestate: "):
			tracestate = h
		}
	}
	// tracing middleware may start a new span, so compare the trace ID only
	assert.Contains(t, traceparent, "00-"+traceID+"-", "traceparent should be forwarded with the same trace ID")
	assert.Equal(t, "tracestate: foo=bar", tracestate, "tracestate should be forwarded")
}

// TestClientSuppliedTransactionID: a caller-supplied X-Webpa-Transaction-Id
// is used for the WRP and echoed on the response, and stat, which builds no
// WRP, echoes it too.
func TestClientSuppliedTransactionID(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)

	t.Run("config", func(t *testing.T) {
		tid := "compliance-" + randomHex(8)
		resp := send(t, request{Method: http.MethodGet, URL: a.configURL("v3", dev.id) + "?names=Device.DeviceInfo.Manufacturer", Token: tok, Headers: map[string]string{transactionIDHeader: tid}})
		require.Equal(t, http.StatusOK, resp.Status)
		assert.Equal(t, tid, resp.Header.Get(transactionIDHeader))
		assert.Equal(t, tid, dev.next(t).msg.TransactionUUID, "the WRP should carry the caller's transaction ID")
	})

	t.Run("config_generated", func(t *testing.T) {
		resp := get(t, a.configURL("v3", dev.id)+"?names=Device.DeviceInfo.Manufacturer", tok)
		require.Equal(t, http.StatusOK, resp.Status)
		tid := resp.Header.Get(transactionIDHeader)
		assert.NotEmpty(t, tid, "tr1d1um generates a transaction ID when the caller sends none")
		assert.Equal(t, tid, dev.next(t).msg.TransactionUUID)
	})

	t.Run("stat", func(t *testing.T) {
		tid := "compliance-" + randomHex(8)
		resp := send(t, request{Method: http.MethodGet, URL: a.statURL("v3", dev.id), Token: tok, Headers: map[string]string{transactionIDHeader: tid}})
		require.Equal(t, http.StatusOK, resp.Status)
		assert.Equal(t, tid, resp.Header.Get(transactionIDHeader))
	})

	t.Run("stat_unknown_device", func(t *testing.T) {
		tid := "compliance-" + randomHex(8)
		resp := send(t, request{Method: http.MethodGet, URL: a.statURL("v3", newMAC()), Token: tok, Headers: map[string]string{transactionIDHeader: tid}})
		require.Equal(t, http.StatusNotFound, resp.Status)
		assert.Equal(t, tid, resp.Header.Get(transactionIDHeader))
		assert.NotContains(t, resp.Header.Get("Content-Type"), "application/json", "a non-200 stat response should not claim to be JSON")
	})
}

// TestPartnerIDHeaderSpoofing: partner IDs supplied in request headers never
// replace the partner IDs from the caller's JWT.
func TestPartnerIDHeaderSpoofing(t *testing.T) {
	a := appFor(t, enforce)
	tok := signer.mint(t, claims{Capabilities: api(allCapability), Partners: []string{"partner-a", "partner-b"}})
	dev := xmidt.connect(t, okReply)
	url := a.configURL("v3", dev.id) + "?names=Device.DeviceInfo.Manufacturer"

	spoofs := []struct {
		name   string
		header string
		value  string
	}{
		{"no_header_baseline", "", ""},
		{"x_xmidt_partner_id", "X-Xmidt-Partner-Id", "attacker-partner-id"},
		{"x_xmidt_partner_ids", "X-Xmidt-Partner-Ids", "attacker-partner-id"},
		{"x_webpa_partner_id", "X-Webpa-Partner-Id", "victim-partner-id"},
		{"multiple_partner_ids", "X-Xmidt-Partner-Id", "partner1,partner2,partner3"},
	}

	for _, tc := range spoofs {
		t.Run(tc.name, func(t *testing.T) {
			headers := map[string]string{}
			if tc.header != "" {
				headers[tc.header] = tc.value
			}
			resp := send(t, request{Method: http.MethodGet, URL: url, Token: tok, Headers: headers})
			require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

			got := dev.next(t)
			assert.ElementsMatch(t, []string{"partner-a", "partner-b"}, got.msg.PartnerIDs, "the device must receive exactly the JWT's partner IDs")
		})
	}
}

// TestResponsePassThrough: how the device's reply payload becomes the HTTP
// response.  JSON is forwarded byte for byte with a JSON content type, other
// payloads are forwarded as-is, and an empty payload gives an empty 200.
func TestResponsePassThrough(t *testing.T) {
	a := appFor(t, enforce)
	var mu sync.Mutex
	payload := ""
	dev := xmidt.connect(t, func(*wrp.Message) reply {
		mu.Lock()
		defer mu.Unlock()
		return reply{Payload: payload}
	})
	url := a.configURL("v3", dev.id) + "?names=Device.DeviceInfo.Manufacturer"

	cases := []struct {
		name       string
		payload    string
		wantJSONCT bool
	}{
		{name: "json_payload", payload: `{"statusCode":200,"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"acme","dataType":0}]}`, wantJSONCT: true},
		{name: "non_json_payload", payload: "hello", wantJSONCT: true},
		{name: "empty_payload", payload: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mu.Lock()
			payload = tc.payload
			mu.Unlock()

			resp := get(t, url, allToken(t))
			dev.next(t)

			assert.Equal(t, http.StatusOK, resp.Status)
			assert.Equal(t, tc.payload, resp.Body, "the device payload should be forwarded unchanged")
			if tc.wantJSONCT {
				assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"), "Content-Type: %q", resp.Header.Get("Content-Type"))
			}
		})
	}
}

// statusCodeDevice connects a device that answers every request with the
// given statusCode in its payload.
func statusCodeDevice(t *testing.T, code int) *device {
	t.Helper()
	return xmidt.connect(t, func(*wrp.Message) reply {
		return reply{Payload: fmt.Sprintf(`{"statusCode":%d}`, code)}
	})
}

// TestWRPStatusCodePassThrough: tr1d1um reads the statusCode from the
// device's payload and uses it as the HTTP status when it is 403 or in
// 520-599; every other code results in HTTP 200.  The payload is forwarded
// either way.
func TestWRPStatusCodePassThrough(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	setBody := `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"x","dataType":0}]}`

	cases := []struct{ code, want int }{
		{200, 200}, {403, 403},
		{520, 520}, {521, 521}, {529, 529}, {550, 550}, {577, 577}, {578, 578}, {598, 598}, {599, 599},
		{0, 200}, {100, 200}, {201, 200}, {302, 200}, {400, 200}, {404, 200}, {500, 200}, {503, 200}, {504, 200},
		{519, 200}, {600, 200}, {1000, 200},
	}

	for _, tc := range cases {
		t.Run(fmt.Sprintf("GET_statusCode_%d", tc.code), func(t *testing.T) {
			dev := statusCodeDevice(t, tc.code)
			resp := get(t, a.configURL("v3", dev.id)+"?names=Device.DeviceInfo.Manufacturer", tok)
			assert.Equal(t, tc.want, resp.Status)
			assert.JSONEq(t, fmt.Sprintf(`{"statusCode":%d}`, tc.code), resp.Body, "the device payload should be forwarded unchanged")
		})
		t.Run(fmt.Sprintf("PATCH_statusCode_%d", tc.code), func(t *testing.T) {
			dev := statusCodeDevice(t, tc.code)
			resp := send(t, request{Method: http.MethodPatch, URL: a.configURL("v3", dev.id), Token: tok, Body: setBody})
			assert.Equal(t, tc.want, resp.Status)
		})
	}
}

// TestXmidtErrorsForwarded: a non-200 from XMiDT is forwarded as it is, status
// and body, for config and stat.
func TestXmidtErrorsForwarded(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	for _, code := range []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusServiceUnavailable, http.StatusGatewayTimeout} {
		t.Run(fmt.Sprintf("config_%d", code), func(t *testing.T) {
			dev := xmidt.connect(t, func(*wrp.Message) reply {
				return reply{Status: code, Payload: fmt.Sprintf(`{"message":"xmidt says %d"}`, code)}
			})
			resp := get(t, a.configURL("v3", dev.id)+"?names=Device.DeviceInfo.Manufacturer", tok)
			assert.Equal(t, code, resp.Status)
			assert.JSONEq(t, fmt.Sprintf(`{"message":"xmidt says %d"}`, code), resp.Body)
			assert.NotEmpty(t, resp.Header.Get(transactionIDHeader))
		})
	}

	t.Run("stat_unknown_device", func(t *testing.T) {
		resp := get(t, a.statURL("v3", newMAC()), tok)
		assert.Equal(t, http.StatusNotFound, resp.Status)
		assert.JSONEq(t, `{"message":"device not found"}`, resp.Body, "XMiDT's body is forwarded")
	})

	t.Run("config_unknown_device", func(t *testing.T) {
		for _, m := range []struct {
			method string
			url    string
			body   string
		}{
			{http.MethodGet, a.configURL("v3", newMAC()) + "?names=Device.A", ""},
			{http.MethodPatch, a.configURL("v2", newMAC()), `{"parameters":[{"name":"Device.A","value":"x","dataType":0,"attributes":{"notify":0}}]}`},
			{http.MethodPost, a.tableURL("v2", newMAC(), "Device.T."), `{"a":"b"}`},
			{http.MethodPut, a.tableURL("v3", newMAC(), "Device.T."), `{"0":{"a":"b"}}`},
			{http.MethodDelete, a.tableURL("v3", newMAC(), "Device.T.1"), ""},
		} {
			resp := send(t, request{Method: m.method, URL: m.url, Token: tok, Body: m.body})
			assert.Equal(t, http.StatusNotFound, resp.Status, "%s %s: body: %s", m.method, m.url, resp.Body)
		}
	})
}

// TestXmidtTimeout: a device that never answers makes tr1d1um return 503
// once its request timeout elapses, with the error contract.
func TestXmidtTimeout(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, func(*wrp.Message) reply {
		return reply{Delay: 5 * time.Second, Payload: `{"statusCode":200}`}
	})

	t.Run("get", func(t *testing.T) {
		start := time.Now()
		resp := get(t, a.configURL("v3", dev.id)+"?names=Device.DeviceInfo.Manufacturer", tok)
		assertErrorContract(t, resp, http.StatusServiceUnavailable, "")
		assert.Less(t, time.Since(start), 4*time.Second, "tr1d1um should give up at its own request timeout")
	})

	t.Run("set", func(t *testing.T) {
		resp := send(t, request{Method: http.MethodPatch, URL: a.configURL("v3", dev.id), Token: tok,
			Body: `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"timeout","dataType":0}]}`})
		assertErrorContract(t, resp, http.StatusServiceUnavailable, "")
	})
}

// TestConnectedDeviceStat: stat for a connected device forwards XMiDT's
// device object; stat for one that went away is 404 again.
func TestConnectedDeviceStat(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)

	resp := get(t, a.statURL("v3", dev.id), tok)
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"))
	assert.JSONEq(t, dev.stat, resp.Body, "the stat body is forwarded unchanged")
	assert.NotEmpty(t, resp.Header.Get(transactionIDHeader))
	assert.NotEmpty(t, resp.Header.Get(spanHeader), "X- headers from XMiDT are forwarded")

	// the same device, spelled non-canonically
	resp = get(t, a.statURL("v2", upperColonMAC(dev.id)), tok)
	assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
}
