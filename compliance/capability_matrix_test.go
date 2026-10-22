// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Endpoint capabilities.
//
// An endpoint capability grants one HTTP method, or all of them, on the
// routes its pattern matches, with the /api/vN prefix removed.  A verified
// token without a capability for the route is an authorization failure
// (403); a 401 would mean the token itself was rejected.
// ---------------------------------------------------------------------------

type capabilityEndpoint struct {
	name       string
	method     string
	path       string
	body       string
	capability []string
	wantStatus int
}

var capabilityScopes = []string{
	allCapability,
	configAllCapability,
	configGetCapability,
	statGetCapability,
	hookPostCapability,
	hooksGetCapability,
}

// capabilityEndpoints lists every route with the capabilities that grant it
// and the status an authorized request to an unknown device gets.
func capabilityEndpoints(t *testing.T, version, id string) []capabilityEndpoint {
	t.Helper()
	base := "/api/" + version + "/device/" + id
	table := "/config/Device.DHCPv4.Server.Pool.1.StaticAddress."
	row := "/config/Device.DHCPv4.Server.Pool.1.StaticAddress.1."

	return []capabilityEndpoint{
		{name: "stat_get", method: http.MethodGet, path: base + "/stat",
			capability: []string{statGetCapability}, wantStatus: http.StatusNotFound},
		{name: "config_get", method: http.MethodGet, path: base + "/config?names=Device.DeviceInfo.Manufacturer",
			capability: []string{configGetCapability, configAllCapability}, wantStatus: http.StatusNotFound},
		{name: "config_patch", method: http.MethodPatch, path: base + "/config",
			body:       `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"compliance","dataType":0}]}`,
			capability: []string{configAllCapability}, wantStatus: http.StatusNotFound},
		{name: "config_table_post", method: http.MethodPost, path: base + table,
			body:       `{"DeviceName":"ComplianceDevice","MACAddress":"DE:AD:BE:EF:00:01"}`,
			capability: []string{configAllCapability}, wantStatus: http.StatusNotFound},
		{name: "config_table_put", method: http.MethodPut, path: base + table,
			body:       `{"0":{"DeviceName":"ComplianceDevice","MACAddress":"DE:AD:BE:EF:00:01"}}`,
			capability: []string{configAllCapability}, wantStatus: http.StatusNotFound},
		{name: "config_row_delete", method: http.MethodDelete, path: base + row,
			capability: []string{configAllCapability}, wantStatus: http.StatusNotFound},
		{name: "hook_post", method: http.MethodPost, path: "/api/" + version + "/hook",
			body:       hookBody(t, version, nil),
			capability: []string{hookPostCapability}, wantStatus: http.StatusOK},
		{name: "hooks_get", method: http.MethodGet, path: "/api/" + version + "/hooks",
			capability: []string{hooksGetCapability}, wantStatus: http.StatusOK},
	}
}

// allowsCapability reports whether a token carrying capability may call the
// endpoint.  The all-capability matches every route.
func allowsCapability(endpoint capabilityEndpoint, capability string) bool {
	return capability == allCapability || slices.Contains(endpoint.capability, capability)
}

// assertReachedEndpoint verifies a response came from the endpoint's
// handler, or from XMiDT, rather than from authorization or routing.
func assertReachedEndpoint(t *testing.T, endpoint capabilityEndpoint, resp apiResponse) {
	t.Helper()
	assert.Equal(t, endpoint.wantStatus, resp.Status, "%s: body: %s", endpoint.name, resp.Body)
	if resp.Status == http.StatusNotFound {
		// the router's own 404 is plain text; a device-not-found 404 is not
		assert.NotContains(t, resp.Body, "404 page not found", "%s: 404 came from the router, not from the device lookup", endpoint.name)
	}
}

// TestCapabilityMatrix: every scope against every endpoint on both API
// versions.  A matching capability reaches the endpoint; any other is a
// 403.
func TestCapabilityMatrix(t *testing.T) {
	a := appFor(t, enforce)

	tokens := make(map[string]string, len(capabilityScopes))
	for _, scope := range capabilityScopes {
		tokens[scope] = token(t, scope)
	}

	for _, version := range []string{"v2", "v3"} {
		for _, endpoint := range capabilityEndpoints(t, version, newMAC()) {
			t.Run(version+"/"+endpoint.name, func(t *testing.T) {
				for _, scope := range capabilityScopes {
					t.Run(strings.NewReplacer(`\b`, "", "/", "_", ".*", "any", ":", "_").Replace(scope), func(t *testing.T) {
						resp := send(t, request{Method: endpoint.method, URL: a.primary + endpoint.path, Token: tokens[scope], Body: endpoint.body})
						if allowsCapability(endpoint, scope) {
							assertReachedEndpoint(t, endpoint, resp)
							return
						}
						assert.Equal(t, http.StatusForbidden, resp.Status, "mismatched capability %q should return 403, body: %s", scope, resp.Body)
					})
				}
			})
		}
	}
}

// TestAlternateServerSameRoutes: the alternate server serves the same API
// behind the same authentication.
func TestAlternateServerSameRoutes(t *testing.T) {
	a := appFor(t, enforce)
	all := allToken(t)
	for _, endpoint := range capabilityEndpoints(t, "v3", newMAC()) {
		t.Run(endpoint.name, func(t *testing.T) {
			resp := send(t, request{Method: endpoint.method, URL: a.alternate + endpoint.path, Token: all, Body: endpoint.body})
			assertReachedEndpoint(t, endpoint, resp)

			resp = send(t, request{Method: endpoint.method, URL: a.alternate + endpoint.path, Body: endpoint.body})
			assert.Equal(t, http.StatusUnauthorized, resp.Status, "the alternate server must authenticate too")
		})
	}
}

// TestCapabilityRegexBoundary: the \b word boundary in a capability keeps it
// from authorizing a longer path segment.  Authorization runs before service
// validation, so the result is 403 rather than the 400 an unsupported
// service would otherwise produce.
func TestCapabilityRegexBoundary(t *testing.T) {
	a := appFor(t, enforce)
	id := newMAC()
	cases := []struct {
		name       string
		capability string
		path       string
	}{
		{"config_all_vs_configX", configAllCapability, "/api/v3/device/" + id + "/configX?names=x"},
		{"stat_get_vs_statX", statGetCapability, "/api/v3/device/" + id + "/statX"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, a.primary+tc.path, token(t, tc.capability))
			assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
		})
	}
}

// TestCapabilityMethodIsExact: a capability for one method grants no other,
// and the all-method value grants every method.
func TestCapabilityMethodIsExact(t *testing.T) {
	a := appFor(t, enforce)
	id := newMAC()
	body := `{"parameters":[{"name":"Device.DeviceInfo.Manufacturer","value":"compliance","dataType":0}]}`

	t.Run("get_capability_does_not_grant_patch", func(t *testing.T) {
		resp := send(t, request{Method: http.MethodPatch, URL: a.configURL("v3", id), Token: token(t, configGetCapability), Body: body})
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("patch_capability_grants_only_patch", func(t *testing.T) {
		tok := token(t, `device/.*/config\b:patch`)
		resp := send(t, request{Method: http.MethodPatch, URL: a.configURL("v3", id), Token: tok, Body: body})
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		resp = get(t, a.configURL("v3", id)+"?names=x", tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("capability_with_unknown_prefix_grants_nothing", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: []string{"other-service:" + allCapability}})
		resp := get(t, a.statURL("v3", id), tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("no_capabilities_at_all", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: []string{}})
		resp := get(t, a.statURL("v3", id), tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})
}

// TestRejectionsAreCounted: an endpoint capability rejection is counted in
// auth_capability_check with the documented reason, and an acceptance with
// outcome accepted.
func TestRejectionsAreCounted(t *testing.T) {
	a := appFor(t, enforce)
	id := newMAC()
	name := checkMetric

	rejected := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": "no_capabilities_match", "clientid": defaultPrincipal, "endpoint": `device/.*/stat\b`, "method": "GET"}, func() {
		resp := get(t, a.statURL("v3", id), token(t, hooksGetCapability))
		assert.Equal(t, http.StatusForbidden, resp.Status)
	})
	assert.Equal(t, 1.0, rejected)

	accepted := counterDelta(t, a, name, map[string]string{"outcome": "accepted", "clientid": defaultPrincipal, "partnerid": defaultPartner, "endpoint": `device/.*/stat\b`, "method": "GET"}, func() {
		resp := get(t, a.statURL("v3", id), token(t, statGetCapability))
		assert.Equal(t, http.StatusNotFound, resp.Status)
	})
	assert.Equal(t, 1.0, accepted)
}
