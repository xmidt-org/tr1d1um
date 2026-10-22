// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/wrp-go/v5"
)

// ---------------------------------------------------------------------------
// Operational behavior: the other servers, response metadata, resource
// limits and the basic-auth deployment.
// ---------------------------------------------------------------------------

// TestHealthEndpoint: the health server answers 200 on /health and nothing
// else, with the configured headers.
func TestHealthEndpoint(t *testing.T) {
	a := appFor(t, enforce)
	resp := get(t, a.health+"/health", "")
	assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	assert.Equal(t, "tr1d1um", resp.Header.Get("X-Midt-Server"))

	assert.Equal(t, http.StatusNotFound, get(t, a.health+"/nope", "").Status)
	assert.Equal(t, http.StatusNotFound, get(t, a.health+"/api/v3/hooks", "").Status, "the API is not served on the health port")
}

// TestResponseMetadataHeaders: every response from the primary server
// carries the configured headers and a transaction ID.
func TestResponseMetadataHeaders(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	for name, resp := range map[string]apiResponse{
		"stat_unknown_device": get(t, a.statURL("v3", newMAC()), tok),
		"config_invalid":      get(t, a.configURL("v3", newMAC()), tok),
		"unauthorized":        get(t, a.statURL("v3", newMAC()), ""),
		"hooks":               get(t, a.hooksURL("v3"), tok),
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, "tr1d1um", resp.Header.Get("X-Midt-Server"))
			assert.Equal(t, "compliance", resp.Header.Get("X-Midt-Version"))
			assert.Regexp(t, `^\d+\.\d+\.\d+`, resp.Header.Get("X-Tr1d1um-Version"))
		})
	}

	t.Run("transaction_id_on_api_responses", func(t *testing.T) {
		for _, resp := range []apiResponse{
			get(t, a.statURL("v3", newMAC()), tok),
			get(t, a.configURL("v3", newMAC()), tok),
		} {
			assert.NotEmpty(t, resp.Header.Get(transactionIDHeader))
		}
	})
}

// TestInternalEndpointsNotExposed: metrics and profiling are served on
// their own ports, never on the public API ports, with or without
// credentials.
func TestInternalEndpointsNotExposed(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	for _, path := range []string{"/metrics", "/debug/pprof/", "/debug/pprof/heap", "/debug/pprof/cmdline", "/debug/vars"} {
		for name, auth := range map[string]string{"anonymous": "", "authenticated": tok} {
			for serverName, base := range map[string]string{"primary": a.primary, "alternate": a.alternate} {
				t.Run(strings.Trim(path, "/")+"/"+serverName+"/"+name, func(t *testing.T) {
					resp := get(t, base+path, auth)
					assert.Equal(t, http.StatusNotFound, resp.Status, "%s must not be served on a public port, body: %.200s", path, resp.Body)
				})
			}
		}
	}

	t.Run("served_on_their_own_ports", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, get(t, a.metrics+"/metrics", "").Status)
		assert.Equal(t, http.StatusOK, get(t, a.pprof+"/debug/pprof/", "").Status)
		assert.Equal(t, http.StatusOK, get(t, a.pprof+"/debug/pprof/cmdline", "").Status)
	})
}

// TestRequestsAreCounted: the request counter records each server's
// responses by code.
func TestRequestsAreCounted(t *testing.T) {
	a := appFor(t, enforce)
	name := requestCountMetric

	for _, tc := range []struct {
		name   string
		server string
		code   string
		do     func()
	}{
		{"primary_401", "server_primary", "401", func() { get(t, a.statURL("v3", newMAC()), "") }},
		{"primary_404", "server_primary", "404", func() { get(t, a.statURL("v3", newMAC()), allToken(t)) }},
		{"primary_400", "server_primary", "400", func() { get(t, a.configURL("v3", newMAC()), allToken(t)) }},
		{"alternate_401", "server_alternate", "401", func() { get(t, a.alternate+"/api/v3/hooks", "") }},
		{"health_200", "server_health", "200", func() { get(t, a.health+"/health", "") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			delta := counterDelta(t, a, name, map[string]string{"server": tc.server, "code": tc.code}, tc.do)
			assert.Equal(t, 1.0, delta)
		})
	}
}

// TestRequestBodyLimit: a request body over the configured limit is
// rejected with 413 before it is read into memory, on every route that
// takes a body.
func TestRequestBodyLimit(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	dev := xmidt.connect(t, okReply)

	// just over the 1 MiB default
	oversized := strings.Repeat("A", 1<<20+1)
	setBody := `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"` + oversized + `","dataType":0}]}`
	rowBody := `{"DeviceName":"` + oversized + `"}`
	rowsBody := `{"0":{"DeviceName":"` + oversized + `"}}`

	devices := make([]string, 0, 70000)
	for range 70000 {
		devices = append(devices, newMAC())
	}
	hookOversized := hookBody(t, "v3", map[string]any{"matcher": map[string]any{"device_id": devices}})
	require.Greater(t, len(hookOversized), 1<<20, "the registration should be over the limit")

	cases := []struct {
		name   string
		method string
		url    string
		body   string
	}{
		{"config_patch", http.MethodPatch, a.configURL("v3", dev.id), setBody},
		{"config_table_post", http.MethodPost, a.tableURL("v3", dev.id, "Device.T."), rowBody},
		{"config_table_put", http.MethodPut, a.tableURL("v3", dev.id, "Device.T."), rowsBody},
		{"hook_v2", http.MethodPost, a.hookURL("v2"), hookOversized},
		{"hook_v3", http.MethodPost, a.hookURL("v3"), hookOversized},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tok, Body: tc.body})
			assert.Equal(t, http.StatusRequestEntityTooLarge, resp.Status, "an oversized body must be rejected with 413, got %d: %.200s", resp.Status, resp.Body)
		})
	}

	t.Run("under_the_limit_is_fine", func(t *testing.T) {
		body := `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"` + strings.Repeat("A", 1<<19) + `","dataType":0}]}`
		resp := send(t, request{Method: http.MethodPatch, URL: a.configURL("v3", dev.id), Token: tok, Body: body})
		assert.Equal(t, http.StatusOK, resp.Status, "body: %.200s", resp.Body)
		dev.next(t)
	})
}

// TestErrorsDoNotLeak: error responses from every layer carry no file
// paths, stack traces, internal addresses or credentials.
func TestErrorsDoNotLeak(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	id := newMAC()

	cases := []struct {
		name    string
		method  string
		url     string
		auth    string
		headers map[string]string
		body    string
		want    []int
	}{
		{name: "malformed_authorization_header", method: http.MethodGet, url: a.statURL("v3", id), auth: "InvalidScheme malformed-token", want: []int{http.StatusUnauthorized}},
		{name: "invalid_base64_basic_auth", method: http.MethodGet, url: a.statURL("v3", id), auth: "Basic not-valid-base64!!!", want: []int{http.StatusUnauthorized}},
		{name: "basic_auth_credentials", method: http.MethodGet, url: a.statURL("v3", id), auth: basic("test", "test"), want: []int{http.StatusUnauthorized}},
		{name: "malformed_json_webhook", method: http.MethodPost, url: a.hookURL("v2"), auth: tok, body: "{invalid json", want: []int{http.StatusBadRequest}},
		{name: "invalid_device_id_format", method: http.MethodGet, url: a.configURL("v3", "not-a-valid-mac-format-at-all") + "?names=Device.DeviceInfo.Manufacturer", auth: tok, want: []int{http.StatusBadRequest}},
		{name: "missing_content_type", method: http.MethodPost, url: a.hookURL("v2"), auth: tok, body: "{}", headers: map[string]string{"Content-Type": ""}, want: []int{http.StatusBadRequest, http.StatusUnsupportedMediaType}},
		{name: "unsupported_http_method", method: http.MethodDelete, url: a.statURL("v3", id), auth: tok, want: []int{http.StatusBadRequest, http.StatusNotFound, http.StatusMethodNotAllowed}},
		{name: "xmidt_unreachable_timeout", method: http.MethodGet, url: a.configURL("v3", xmidt.connect(t, func(*wrp.Message) reply { return reply{Delay: 5e9} }).id) + "?names=x", auth: tok, want: []int{http.StatusServiceUnavailable}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tc.auth, Headers: tc.headers, Body: tc.body})
			assert.Contains(t, tc.want, resp.Status, "body: %s", resp.Body)
			assertNoLeak(t, resp.Body)
			assert.NotContains(t, resp.Body, xmidt.URL(), "the XMiDT address must not be shown to callers")
			assert.NotContains(t, resp.Body, argus.URL(), "the Argus address must not be shown to callers")
			assert.NotContains(t, resp.Header.Get("Server"), "/", "the Server header must not reveal a version")
		})
	}
}

// ---------------------------------------------------------------------------
// The basic-auth deployment: for local testing, a configured basic
// credential is accepted and carries no capabilities and no partners.
// ---------------------------------------------------------------------------

// TestBasicAuthProfile: the configured credential works, anything else is
// refused, and bearer tokens are not accepted at all.
func TestBasicAuthProfile(t *testing.T) {
	a := appFor(t, basicAuth)
	configured := "Basic " + basicCredential

	t.Run("configured_credential_reaches_the_handler", func(t *testing.T) {
		resp := get(t, a.statURL("v3", newMAC()), configured)
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
	})

	for name, auth := range map[string]string{
		"no_credentials": "",
		"wrong_password": basic("user", "nope"),
		"unknown_user":   basic("nope", "pass"),
		"empty_user":     basic("", "pass"),
		"bearer_token":   allToken(t),
		"not_base64":     "Basic not-valid-base64!!!",
	} {
		t.Run(name, func(t *testing.T) {
			resp := get(t, a.statURL("v3", newMAC()), auth)
			// a header that cannot be parsed at all is a 400
			assert.Contains(t, []int{http.StatusUnauthorized, http.StatusBadRequest}, resp.Status, "body: %s", resp.Body)
			assert.NotContains(t, resp.Body, "nope")
		})
	}

	t.Run("no_capability_checks", func(t *testing.T) {
		// a basic credential is not subject to endpoint capabilities
		for _, endpoint := range capabilityEndpoints(t, "v3", newMAC()) {
			if endpoint.name == "hook_post" || endpoint.name == "hooks_get" {
				continue
			}
			resp := send(t, request{Method: endpoint.method, URL: a.primary + endpoint.path, Token: configured, Body: endpoint.body})
			assertReachedEndpoint(t, endpoint, resp)
		}
	})

	t.Run("partner_ids_come_from_the_header", func(t *testing.T) {
		dev := xmidt.connect(t, okReply)
		resp := send(t, request{Method: http.MethodGet, URL: a.configURL("v3", dev.id) + "?names=Device.A", Token: configured,
			Headers: map[string]string{"X-Xmidt-Partner-Id": "partner-x, partner-y"}})
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.ElementsMatch(t, []string{"partner-x", "partner-y"}, dev.next(t).msg.PartnerIDs, "a basic caller names its partners in the header")

		resp = get(t, a.configURL("v3", dev.id)+"?names=Device.A", configured)
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Empty(t, dev.next(t).msg.PartnerIDs, "without the header there are no partners")
	})

	t.Run("webhooks", func(t *testing.T) {
		hookURL := uniqueListenerURL()
		body := hookBody(t, "v3", map[string]any{"config": map[string]any{"url": hookURL, "content_type": "application/json"}})
		resp := send(t, request{Method: http.MethodPost, URL: a.hookURL("v3"), Token: configured, Body: body,
			Headers: map[string]string{"X-Xmidt-Partner-Ids": "partner-x"}})
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

		list := send(t, request{Method: http.MethodGet, URL: a.hooksURL("v3"), Token: configured, Headers: map[string]string{"X-Xmidt-Partner-Ids": "partner-x"}})
		require.Equal(t, http.StatusOK, list.Status, "body: %s", list.Body)
		var hooks []listedHook
		require.NoError(t, json.Unmarshal([]byte(list.Body), &hooks))
		assert.Contains(t, hookURLs(hooks), hookURL)
	})

	t.Run("no_capability_warning_header", func(t *testing.T) {
		resp := get(t, a.statURL("v3", newMAC()), configured)
		assert.Empty(t, resp.Header.Values(warningHeader))
	})
}

// TestNoCapabilityWarningForStandardToken: a token with only endpoint
// capabilities never raises a capability warning, in any mode.
func TestNoCapabilityWarningForStandardToken(t *testing.T) {
	for _, p := range []profile{enforce, warn, silent} {
		t.Run(p.name, func(t *testing.T) {
			a := appFor(t, p)
			resp := get(t, a.statURL("v3", newMAC()), allToken(t))
			assert.Equal(t, http.StatusNotFound, resp.Status)
			assert.Empty(t, resp.Header.Values(warningHeader), "a standard token should not raise capability warnings")
		})
	}
}

// TestStartupRefusesBadConfiguration: tr1d1um refuses to start with both
// or neither authentication method configured, as its configuration
// promises.
func TestStartupRefusesBadConfiguration(t *testing.T) {
	for name, overrides := range map[string]map[string]any{
		"basic_and_jwt":  {"auth": map[string]any{"inbound": map[string]any{"basic": []string{basicCredential}}}},
		"neither":        {"auth": map[string]any{"inbound": map[string]any{"jwt": nil}}},
		"bad_wrp_source": {"WRPSource": "not a locator"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := startApp(profile{name: "bad-" + name, overrides: overrides})
			require.Error(t, err, "tr1d1um must refuse to start")
			assert.Contains(t, err.Error(), "exited before it was healthy")
		})
	}
}
