// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Request validation: what tr1d1um rejects before anything reaches XMiDT,
// and how.  Every rejection is a 400 with the error contract.
// ---------------------------------------------------------------------------

func TestRequestValidation(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	id := newMAC()
	config := a.configURL("v3", id)
	table := a.tableURL("v3", id, "Device.X_COMPLIANCE.Table.")
	validSet := `{"parameters":[{"name":"Device.A","value":"x","dataType":0}]}`
	notAService := a.primary + "/api/v3/device/" + id + "/notaservice"

	cases := []struct {
		name    string
		method  string
		url     string
		headers map[string]string
		body    string
		message string
	}{
		{name: "get_without_names", method: http.MethodGet, url: config, message: "names parameter is required"},
		{name: "get_with_empty_names", method: http.MethodGet, url: config + "?names=", message: "names parameter is required"},

		{name: "unsupported_service_get", method: http.MethodGet, url: notAService + "?names=x", message: "unsupported Service"},
		{name: "unsupported_service_patch", method: http.MethodPatch, url: notAService, body: validSet, message: "unsupported Service"},
		{name: "unsupported_service_post", method: http.MethodPost, url: notAService + "/x", body: `{"a":"b"}`, message: "unsupported Service"},
		{name: "unsupported_service_put", method: http.MethodPut, url: notAService + "/x", body: `{"0":{"a":"b"}}`, message: "unsupported Service"},
		{name: "unsupported_service_delete", method: http.MethodDelete, url: notAService + "/x", message: "unsupported Service"},

		{name: "invalid_device_id", method: http.MethodGet, url: a.configURL("v3", "not-a-mac") + "?names=x"},
		{name: "invalid_device_id_stat", method: http.MethodGet, url: a.statURL("v3", "not-a-mac")},

		{name: "patch_malformed_json", method: http.MethodPatch, url: config, body: `{bad`, message: "invalid WDMP structure"},
		{name: "patch_empty_body", method: http.MethodPatch, url: config, message: "invalid SET message"},
		{name: "patch_empty_parameters", method: http.MethodPatch, url: config, body: `{"parameters":[]}`, message: "invalid SET message"},
		{name: "patch_missing_name", method: http.MethodPatch, url: config, body: `{"parameters":[{"value":"x","dataType":0}]}`, message: "invalid SET message"},
		{name: "patch_empty_name", method: http.MethodPatch, url: config, body: `{"parameters":[{"name":"","value":"x","dataType":0}]}`, message: "invalid SET message"},
		{name: "patch_value_without_datatype", method: http.MethodPatch, url: config, body: `{"parameters":[{"name":"A","value":"x"}]}`, message: "invalid SET message"},
		{name: "patch_negative_datatype", method: http.MethodPatch, url: config, body: `{"parameters":[{"name":"A","value":"x","dataType":-1}]}`, message: "invalid SET message"},
		{name: "patch_mixed_set_and_set_attributes", method: http.MethodPatch, url: config,
			body:    `{"parameters":[{"name":"A","attributes":{"notify":1}},{"name":"B","value":"x","dataType":0}]}`,
			message: "invalid SET message"},
		{name: "patch_old_cid_without_new_cid", method: http.MethodPatch, url: config, body: validSet,
			headers: map[string]string{"X-Webpa-Sync-Old-Cid": "old"}, message: "newCid is required for TEST_AND_SET"},

		{name: "post_table_empty_body", method: http.MethodPost, url: table, message: "row property is required"},
		{name: "post_table_row_is_array", method: http.MethodPost, url: table, body: `[1,2]`, message: "row property is invalid"},
		{name: "post_table_row_not_strings", method: http.MethodPost, url: table, body: `{"a":1}`, message: "row property is invalid"},
		{name: "put_table_empty_body", method: http.MethodPut, url: table, message: "rows property is required"},
		{name: "put_table_row_not_object", method: http.MethodPut, url: table, body: `{"0":"notamap"}`, message: "rows property is invalid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tok, Headers: tc.headers, Body: tc.body})
			assertErrorContract(t, resp, http.StatusBadRequest, tc.message)
			assertNoLeak(t, resp.Body)
		})
	}
}

// TestMethodNotSupported: methods a route does not serve are rejected with a
// client error, never executed.  The router answers 404 or 405 depending on
// route layout, and PATCH on /stat is routed to the config handler where
// "stat" is an unsupported service (400); all are rejections.
func TestMethodNotSupported(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)
	row := "Device.X_COMPLIANCE.Table.1"

	cases := []struct {
		name   string
		method string
		url    string
	}{
		{"put_config", http.MethodPut, a.configURL("v3", dev.id)},
		{"post_config", http.MethodPost, a.configURL("v3", dev.id)},
		{"delete_config", http.MethodDelete, a.configURL("v3", dev.id)},
		{"get_config_row", http.MethodGet, a.tableURL("v3", dev.id, row)},
		{"patch_config_row", http.MethodPatch, a.tableURL("v3", dev.id, row)},
		{"post_stat", http.MethodPost, a.statURL("v3", dev.id)},
		{"patch_stat", http.MethodPatch, a.statURL("v3", dev.id)},
		{"delete_stat", http.MethodDelete, a.statURL("v3", dev.id)},
		{"get_hook", http.MethodGet, a.hookURL("v3")},
		{"post_hooks", http.MethodPost, a.hooksURL("v3")},
		{"delete_hooks", http.MethodDelete, a.hooksURL("v3")},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tok})
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed}, resp.Status,
				"%s %s must be rejected, got %d", tc.method, tc.url, resp.Status)
			dev.none(t)
		})
	}
}

// ---------------------------------------------------------------------------
// Paths and device IDs.
// ---------------------------------------------------------------------------

// TestDeviceIDValidation: what counts as a device ID.  An accepted ID
// reaches the device lookup (404 for an unknown device); a rejected one is
// a 400.
func TestDeviceIDValidation(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	cases := []struct {
		name string
		id   string
		want int
	}{
		{"canonical", "mac:112233445566", http.StatusNotFound},
		{"colons", "mac:11:22:33:44:55:66", http.StatusNotFound},
		{"upper_case", "MAC:112233AABBCC", http.StatusNotFound},
		{"serial", "serial:ABC-123-XYZ", http.StatusNotFound},
		{"uuid", "uuid:550e8400-e29b-41d4-a716-446655440000", http.StatusNotFound},
		{"too_long", "mac:112233445566778899", http.StatusBadRequest},
		{"too_short", "mac:123456", http.StatusBadRequest},
		{"spaces", "mac:11 22 33 44 55 66", http.StatusBadRequest},
		{"unicode", "mac:αβγδεζηθικλμ", http.StatusBadRequest},
		{"no_scheme", "112233445566", http.StatusBadRequest},
		{"unknown_scheme", "nope:112233445566", http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			escaped := url.PathEscape(tc.id)
			resp := get(t, a.statURL("v3", escaped), tok)
			assert.Equal(t, tc.want, resp.Status, "stat: body: %s", resp.Body)
			if tc.want == http.StatusBadRequest {
				assertErrorContract(t, resp, http.StatusBadRequest, "")
			}

			resp = get(t, a.configURL("v3", escaped)+"?names=Device.DeviceInfo.Manufacturer", tok)
			assert.Equal(t, tc.want, resp.Status, "config: body: %s", resp.Body)
		})
	}
}

// TestUnsupportedAPIVersion: an API version other than v2 or v3 is not
// routed.
func TestUnsupportedAPIVersion(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	for _, version := range []string{"v0", "v1", "v4", "v33", "v"} {
		t.Run(version, func(t *testing.T) {
			resp := get(t, a.statURL(version, "mac:112233445566"), tok)
			assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		})
	}
}

// TestUnroutedPaths: paths outside the API are 404, whatever the token.
func TestUnroutedPaths(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	for name, path := range map[string]string{
		"empty":    "",
		"root":     "/",
		"api":      "/api",
		"api_v3":   "/api/v3",
		"device":   "/api/v3/device",
		"long_url": "/" + strings.Repeat("a", 1000),
	} {
		t.Run(name, func(t *testing.T) {
			resp := get(t, a.primary+path, tok)
			assert.Contains(t, []int{http.StatusNotFound, http.StatusRequestURITooLong}, resp.Status, "body: %s", resp.Body)
		})
	}
}

// TestPathTraversal: traversal attempts, literal and percent-encoded, never
// reach a file or a different route.  They are rejected or redirected to a
// cleaned path, never served and never a server error.
func TestPathTraversal(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	targets := []struct {
		name string
		path string
	}{
		{"direct_file_path", "/etc/tr1d1um/tr1d1um.yaml"},
		{"dotdot_to_etc", "/api/v3/../../../../etc/passwd"},
		{"dotdot_in_device", "/api/v3/device/../../../../etc/passwd/stat"},
		{"encoded_dotdot", "/api/v3/%2e%2e/%2e%2e/%2e%2e/etc/passwd"},
		{"encoded_slash_dotdot", "/api/v3/device/..%2f..%2f..%2fetc%2fpasswd/stat"},
		{"double_encoded_dotdot", "/api/v3/%252e%252e/%252e%252e/etc/passwd"},
		{"backslash_dotdot", "/api/v3/..%5c..%5c..%5cetc%5cpasswd"},
	}

	for _, tc := range targets {
		t.Run(tc.name, func(t *testing.T) {
			resp := rawRequest(t, a, http.MethodGet, tc.path, tok)
			assert.NotContains(t, resp.Body, "root:", "response must not contain file contents")
			assert.NotContains(t, resp.Body, "targetURL", "response must not contain tr1d1um configuration")
			assert.Contains(t, []int{http.StatusBadRequest, http.StatusNotFound, http.StatusMovedPermanently, http.StatusPermanentRedirect}, resp.Status,
				"%s should be rejected or redirected, got %d", tc.path, resp.Status)
		})
	}
}

// injectionBaseMAC is a syntactically valid ID the injection payloads are
// appended to, so the injected characters alone decide the outcome.
const injectionBaseMAC = "mac:020000aabbcc"

type injectionCase struct {
	name     string
	deviceID string
	// redirectOK allows the router's redirect to a cleaned path, which is a
	// safe outcome for dot-segment payloads.
	redirectOK bool
}

var injectionCases = []injectionCase{
	{name: "path_traversal_dotdot", deviceID: injectionBaseMAC + "/../admin", redirectOK: true},
	{name: "path_injection_slash", deviceID: injectionBaseMAC + "/extra/path"},
	{name: "query_injection", deviceID: injectionBaseMAC + "?admin=true"},
	{name: "fragment_injection", deviceID: injectionBaseMAC + "%23admin"},
	{name: "url_encoded_slash", deviceID: injectionBaseMAC + "%2Fadmin"},
	{name: "encoded_dotdot", deviceID: injectionBaseMAC + "%2F%2E%2E%2Fadmin", redirectOK: true},
	{name: "newline_injection", deviceID: injectionBaseMAC + "%0aX-Admin:%20true"},
	{name: "crlf_injection", deviceID: injectionBaseMAC + "%0d%0aX-Admin:%20true"},
	{name: "null_byte", deviceID: injectionBaseMAC + "%00admin"},
	{name: "semicolon_injection", deviceID: injectionBaseMAC + ";admin=true"},
	{name: "double_encoded_slash", deviceID: injectionBaseMAC + "%252Fadmin"},
	{name: "double_encoded_dotdot", deviceID: injectionBaseMAC + "%252E%252E%252Fadmin"},
	{name: "double_encoded_question", deviceID: injectionBaseMAC + "%253Fadmin=true"},
}

// TestDeviceIDURLInjection: device IDs containing URL metacharacters,
// control characters or encodings are rejected or safely handled on the
// stat and config routes, written to the socket exactly as given.
func TestDeviceIDURLInjection(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	for _, route := range []struct {
		name   string
		target func(id string) string
	}{
		{"stat", func(id string) string { return "/api/v3/device/" + id + "/stat" }},
		{"config", func(id string) string { return "/api/v3/device/" + id + "/config?names=Device.DeviceInfo.Manufacturer" }},
	} {
		t.Run(route.name+"/control_valid_id", func(t *testing.T) {
			resp := rawRequest(t, a, http.MethodGet, route.target(injectionBaseMAC), tok)
			assert.Equal(t, http.StatusNotFound, resp.Status, "a valid, unconnected device ID must reach the device lookup; otherwise the cases below prove nothing")
		})
		for _, tc := range injectionCases {
			t.Run(route.name+"/"+tc.name, func(t *testing.T) {
				resp := rawRequest(t, a, http.MethodGet, route.target(tc.deviceID), tok)
				allowed := []int{http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed}
				if tc.redirectOK {
					allowed = append(allowed, http.StatusMovedPermanently, http.StatusPermanentRedirect)
				}
				assert.Contains(t, allowed, resp.Status, "%s should be rejected or safely handled, got %d. Body: %s", tc.name, resp.Status, resp.Body)
				assert.Less(t, resp.Status, 500, "injection must never cause a server error")
				assert.Empty(t, resp.Header.Get("X-Admin"), "a header must not be injected")
			})
		}
	}
}

// TestExtraHeadersHandled: unknown request headers do not break a route.
func TestExtraHeadersHandled(t *testing.T) {
	a := appFor(t, enforce)
	headers := map[string]string{}
	for i := range 10 {
		headers[fmt.Sprintf("X-Compliance-Extra-%d", i)] = fmt.Sprintf("extra-value-%d", i)
	}
	resp := send(t, request{Method: http.MethodGet, URL: a.statURL("v3", newMAC()), Token: allToken(t), Headers: headers})
	assert.Equal(t, http.StatusNotFound, resp.Status, "extra request headers should not break the endpoint")
}
