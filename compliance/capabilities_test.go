// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// The narrowing capability kinds: CIDR, rate and parameter, plus the
// parameter capability age limit.  Each kind runs in a configured mode;
// the profiles in harness_test.go cover them.  docs/capabilities.md is the
// contract these tests pin: the status codes, the warning header and the
// metrics.
// ---------------------------------------------------------------------------

// The caller origin of every request is the loopback peer, so these
// networks include and exclude it.
const (
	includedNetwork = "127.0.0.0/8"
	excludedNetwork = "192.0.2.0/24"
	malformedCIDR   = "10.0.0.1" // a bare address, no /bits
)

// withCIDR mints a token for every endpoint that also carries CIDR
// capabilities.
func withCIDR(t *testing.T, sub string, networks ...string) string {
	t.Helper()
	caps := api(allCapability)
	for _, n := range networks {
		caps = append(caps, cidrPrefix+n)
	}

	return signer.mint(t, claims{Subject: sub, Capabilities: caps})
}

// warnings returns the capability warnings on a response.
func warnings(resp apiResponse) []string {
	return resp.Header.Values(warningHeader)
}

// assertWarning asserts that one of the warnings has the reason and every
// key=value given.
func assertWarning(t *testing.T, resp apiResponse, reason string, attrs ...string) {
	t.Helper()
	for _, w := range warnings(resp) {
		if !strings.HasPrefix(w, reason+";") && w != reason {
			continue
		}
		ok := true
		for _, attr := range attrs {
			if !strings.Contains(w, attr) {
				ok = false
			}
		}
		if ok {
			return
		}
	}
	t.Errorf("no %s warning with %v among %q", reason, attrs, warnings(resp))
}

// warningsWithReason counts the warnings that start with reason.
func warningsWithReason(resp apiResponse, reason string) int {
	var n int
	for _, w := range warnings(resp) {
		if strings.HasPrefix(w, reason+";") || w == reason {
			n++
		}
	}

	return n
}

// ---------------------------------------------------------------------------
// CIDR
// ---------------------------------------------------------------------------

// TestCIDREnforce: in enforce mode a token's networks must include the
// caller origin; a token without any is unrestricted; a malformed
// capability counts as present, matches nothing, and is reported.
func TestCIDREnforce(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())

	t.Run("included_network_is_allowed", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "", includedNetwork))
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		assert.Empty(t, warnings(resp))
	})

	t.Run("one_of_several_networks_is_enough", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "", excludedNetwork, includedNetwork))
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
	})

	t.Run("excluded_network_is_refused", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "", excludedNetwork))
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
		assert.Equal(t, 0, warningsWithReason(resp, "would-reject"), "enforce rejects rather than warning")
	})

	t.Run("malformed_capability_is_refused_and_reported", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "", malformedCIDR))
		assert.Equal(t, http.StatusForbidden, resp.Status, "a malformed capability still counts as present and matches nothing, body: %s", resp.Body)
		assertWarning(t, resp, "malformed", "kind=cidr", malformedCIDR)
	})

	t.Run("malformed_beside_a_good_one_is_reported_but_allowed", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "", malformedCIDR, includedNetwork))
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		assertWarning(t, resp, "malformed", "kind=cidr")
	})

	t.Run("no_cidr_capability_is_unrestricted", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, ""))
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		assert.Empty(t, warnings(resp))
	})

	t.Run("forwarding_headers_from_an_untrusted_peer_are_ignored", func(t *testing.T) {
		// 192.0.2.10 is inside the excluded network; a header from a peer
		// that is not a trusted proxy must not move the origin there
		headers := map[string]string{"X-Forwarded-For": "192.0.2.10", "Forwarded": "for=192.0.2.10"}
		resp := send(t, request{Method: http.MethodGet, URL: url, Token: withCIDR(t, "", excludedNetwork), Headers: headers})
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)

		resp = send(t, request{Method: http.MethodGet, URL: url, Token: withCIDR(t, "", includedNetwork), Headers: headers})
		assert.Equal(t, http.StatusNotFound, resp.Status, "a spoofed header must not make the loopback peer look excluded, body: %s", resp.Body)
	})

	t.Run("applies_to_every_route", func(t *testing.T) {
		tok := withCIDR(t, "", excludedNetwork)
		for _, endpoint := range capabilityEndpoints(t, "v3", newMAC()) {
			resp := send(t, request{Method: endpoint.method, URL: a.primary + endpoint.path, Token: tok, Body: endpoint.body})
			assert.Equal(t, http.StatusForbidden, resp.Status, "%s: body: %s", endpoint.name, resp.Body)
		}
	})
}

// TestCIDROverrides: an override replaces the token's networks, and an
// empty override leaves the caller unrestricted.
func TestCIDROverrides(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())

	t.Run("override_narrows_a_permissive_token", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "cidr-override-caller", includedNetwork))
		assert.Equal(t, http.StatusForbidden, resp.Status, "the override names only the excluded network, body: %s", resp.Body)
	})

	t.Run("override_is_matched_without_regard_to_case", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "CIDR-Override-Caller", includedNetwork))
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("empty_override_exempts_a_restricted_token", func(t *testing.T) {
		resp := get(t, url, withCIDR(t, "cidr-exempt-caller", excludedNetwork))
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
	})

	t.Run("empty_override_in_required_mode_refuses", func(t *testing.T) {
		r := appFor(t, required)
		resp := get(t, r.statURL("v3", newMAC()), withCIDR(t, "cidr-exempt-caller", includedNetwork))
		assert.Equal(t, http.StatusForbidden, resp.Status, "an empty override leaves the caller with no network, body: %s", resp.Body)
	})
}

// TestCIDRRequired: required mode also refuses a token with no CIDR
// capability.
func TestCIDRRequired(t *testing.T) {
	a := appFor(t, required)
	url := a.statURL("v3", newMAC())

	resp := get(t, url, withCIDR(t, "", includedNetwork))
	assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)

	resp = get(t, url, withCIDR(t, ""))
	assert.Equal(t, http.StatusForbidden, resp.Status, "a token with no CIDR capability is refused, body: %s", resp.Body)

	resp = get(t, url, withCIDR(t, "", excludedNetwork))
	assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
}

// TestCIDRPermissiveModes: the permissive modes reject nothing.  The ones
// with warnings tell the caller what enforce or required would have
// rejected; the silent ones tell nothing.  All of them count.
func TestCIDRPermissiveModes(t *testing.T) {
	for _, tc := range []struct {
		profile  profile
		warns    bool
		required bool
	}{
		{warn, true, false},
		{silent, false, false},
		{requiredWarn, true, true},
		{requiredSilent, false, true},
	} {
		t.Run(tc.profile.name, func(t *testing.T) {
			a := appFor(t, tc.profile)
			url := a.statURL("v3", newMAC())
			sub := "cidr-" + tc.profile.name + "-" + randomHex(4)
			checkName := checkMetric
			warningName := warningMetric

			t.Run("included", func(t *testing.T) {
				resp := get(t, url, withCIDR(t, sub, includedNetwork))
				assert.Equal(t, http.StatusNotFound, resp.Status)
				assert.Empty(t, warnings(resp))
			})

			t.Run("excluded", func(t *testing.T) {
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "cidr", "reason": "origin_not_allowed", "clientid": sub}, func() {
					resp = get(t, url, withCIDR(t, sub, excludedNetwork))
				})
				assert.Equal(t, http.StatusNotFound, resp.Status, "permissive must let the request through, body: %s", resp.Body)
				if tc.warns {
					assertWarning(t, resp, "would-reject", "kind=cidr", "reason=origin-not-allowed", "origin=127.0.0.1")
				} else {
					assert.Empty(t, warnings(resp), "a silent mode tells the caller nothing")
				}
				assert.Equal(t, 1.0, counted, "the would-be rejection is counted as a warning")
				assert.Zero(t, counter(scrape(t, a), checkName, map[string]string{"outcome": "rejected", "clientid": sub}), "nothing is rejected")
			})

			t.Run("malformed", func(t *testing.T) {
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "cidr", "reason": "malformed", "clientid": sub}, func() {
					resp = get(t, url, withCIDR(t, sub, malformedCIDR))
				})
				assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
				if tc.warns {
					assertWarning(t, resp, "malformed", "kind=cidr")
				} else {
					assert.Empty(t, warnings(resp))
				}
				assert.Equal(t, 1.0, counted)
			})

			t.Run("none", func(t *testing.T) {
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "cidr", "reason": "no_cidr_capability", "clientid": sub}, func() {
					resp = get(t, url, withCIDR(t, sub))
				})
				assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
				if tc.required && tc.warns {
					assertWarning(t, resp, "would-reject", "kind=cidr", "reason=no-cidr-capability")
				} else {
					assert.Empty(t, warnings(resp))
				}
				if tc.required {
					assert.Equal(t, 1.0, counted, "a trial run of required counts the missing capability")
				} else {
					assert.Zero(t, counted)
				}
			})
		})
	}
}

// TestCIDRTrustedProxies: when the peer is a trusted proxy, the forwarding
// headers choose the caller origin, walked from the right past every
// trusted proxy.
func TestCIDRTrustedProxies(t *testing.T) {
	a := appFor(t, proxied)
	url := a.statURL("v3", newMAC())
	// a network that the forwarded address is in and the peer is not
	forwardedNetwork := "203.0.113.0/24"

	cases := []struct {
		name     string
		networks []string
		headers  map[string]string
		want     int
	}{
		{"forwarded_for_chooses_the_origin", []string{forwardedNetwork}, map[string]string{"Forwarded": "for=203.0.113.9"}, http.StatusNotFound},
		{"x_forwarded_for_chooses_the_origin", []string{forwardedNetwork}, map[string]string{"X-Forwarded-For": "203.0.113.9"}, http.StatusNotFound},
		{"forwarded_wins_over_x_forwarded_for", []string{forwardedNetwork}, map[string]string{"Forwarded": "for=203.0.113.9", "X-Forwarded-For": "192.0.2.9"}, http.StatusNotFound},
		{"trusted_hops_are_walked_past", []string{forwardedNetwork}, map[string]string{"X-Forwarded-For": "203.0.113.9, 127.0.0.5, 127.0.0.6"}, http.StatusNotFound},
		{"the_nearest_untrusted_hop_is_the_origin", []string{forwardedNetwork}, map[string]string{"X-Forwarded-For": "203.0.113.9, 192.0.2.9"}, http.StatusForbidden},
		{"origin_outside_the_token", []string{forwardedNetwork}, map[string]string{"X-Forwarded-For": "192.0.2.9"}, http.StatusForbidden},
		// the peer is a trusted proxy that reported nothing, so there is no
		// hop that is not a proxy: the origin is unknown
		{"trusted_peer_without_header_is_unknown", []string{includedNetwork}, nil, http.StatusForbidden},
		{"loopback_token_does_not_cover_a_forwarded_origin", []string{includedNetwork}, map[string]string{"X-Forwarded-For": "203.0.113.9"}, http.StatusForbidden},
		{"unparseable_origin_is_refused", []string{forwardedNetwork}, map[string]string{"X-Forwarded-For": "not-an-address"}, http.StatusForbidden},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: http.MethodGet, URL: url, Token: withCIDR(t, "", tc.networks...), Headers: tc.headers})
			assert.Equal(t, tc.want, resp.Status, "body: %s, warnings: %v", resp.Body, warnings(resp))
		})
	}
}

// TestCIDRRejectionsAreCounted: each CIDR refusal is counted with its
// documented reason.
func TestCIDRRejectionsAreCounted(t *testing.T) {
	a := appFor(t, required)
	url := a.statURL("v3", newMAC())
	name := checkMetric
	sub := "cidr-count-" + randomHex(4)

	for reason, tok := range map[string]string{
		"origin_not_allowed": withCIDR(t, sub, excludedNetwork),
		"no_cidr_capability": withCIDR(t, sub),
	} {
		t.Run(reason, func(t *testing.T) {
			delta := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": reason, "clientid": sub}, func() {
				assert.Equal(t, http.StatusForbidden, get(t, url, tok).Status)
			})
			assert.Equal(t, 1.0, delta)
		})
	}
}

// ---------------------------------------------------------------------------
// Rate
// ---------------------------------------------------------------------------

// withRate mints a token for every endpoint that also carries rate
// capabilities.
func withRate(t *testing.T, sub string, rates ...string) string {
	t.Helper()
	caps := api(allCapability)
	for _, r := range rates {
		caps = append(caps, ratePrefix+r)
	}

	return signer.mint(t, claims{Subject: sub, Capabilities: caps})
}

// burst sends n requests and returns the statuses seen, in order, with the
// last response.
func burst(t *testing.T, url, tok string, n int) ([]int, apiResponse) {
	t.Helper()
	statuses := make([]int, 0, n)
	var last apiResponse
	for range n {
		last = get(t, url, tok)
		statuses = append(statuses, last.Status)
	}

	return statuses, last
}

// TestRateEnforce: in enforce mode a caller is held to the rates its
// tokens carry, per principal; over the rate is a 429 with Retry-After.
func TestRateEnforce(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())

	t.Run("over_the_rate_is_429_with_retry_after", func(t *testing.T) {
		sub := "rate-" + randomHex(4)
		statuses, last := burst(t, url, withRate(t, sub, "3/1m"), 4)
		assert.Equal(t, []int{404, 404, 404, 429}, statuses)
		retryAfter, err := strconv.Atoi(last.Header.Get("Retry-After"))
		require.NoError(t, err, "Retry-After should be whole seconds, got %q", last.Header.Get("Retry-After"))
		assert.Positive(t, retryAfter)
		assert.LessOrEqual(t, retryAfter, 60)
	})

	t.Run("the_rate_is_per_principal", func(t *testing.T) {
		first, second := "rate-"+randomHex(4), "rate-"+randomHex(4)
		statuses, _ := burst(t, url, withRate(t, first, "2/1m"), 2)
		assert.Equal(t, []int{404, 404}, statuses)
		statuses, _ = burst(t, url, withRate(t, second, "2/1m"), 2)
		assert.Equal(t, []int{404, 404}, statuses, "another principal has its own allowance")
		statuses, _ = burst(t, url, withRate(t, first, "2/1m"), 1)
		assert.Equal(t, []int{429}, statuses, "the first principal is still over")
	})

	t.Run("every_recent_rate_of_the_caller_applies", func(t *testing.T) {
		sub := "rate-" + randomHex(4)
		// a stricter token seen once holds the caller even when a looser one is used
		statuses, _ := burst(t, url, withRate(t, sub, "1/1m"), 1)
		assert.Equal(t, []int{404}, statuses)
		statuses, _ = burst(t, url, withRate(t, sub, "100/1m"), 1)
		assert.Equal(t, []int{429}, statuses, "a cut takes effect on the next request whichever token carries it")
	})

	t.Run("rate_counts_rejections_elsewhere_too", func(t *testing.T) {
		sub := "rate-" + randomHex(4)
		tok := withRate(t, sub, "2/1m")
		statuses, _ := burst(t, a.hooksURL("v3"), tok, 3)
		assert.Equal(t, 429, statuses[2], "the rate covers every route: %v", statuses)
	})

	t.Run("no_rate_capability_is_unrestricted", func(t *testing.T) {
		statuses, last := burst(t, url, withRate(t, "rate-"+randomHex(4)), 10)
		for _, s := range statuses {
			assert.Equal(t, 404, s)
		}
		assert.Empty(t, warnings(last))
	})

	t.Run("malformed_rate_allows_nothing", func(t *testing.T) {
		sub := "rate-" + randomHex(4)
		resp := get(t, url, withRate(t, sub, "100/1d"))
		assert.Equal(t, http.StatusForbidden, resp.Status, "a malformed rate is held to zero, body: %s", resp.Body)
		assertWarning(t, resp, "malformed", "kind=rate", "100/1d")
	})

	t.Run("short_window_is_rescaled_to_the_minimum", func(t *testing.T) {
		// 2/1s is held as 120/1m, so three quick calls are fine
		statuses, _ := burst(t, url, withRate(t, "rate-"+randomHex(4), "2/1s"), 3)
		assert.Equal(t, []int{404, 404, 404}, statuses)
	})
}

// TestRateOverridesAndDefaults: an override replaces the token's rates,
// an empty override exempts, and defaults hold a caller with no rate.
func TestRateOverridesAndDefaults(t *testing.T) {
	t.Run("override_replaces_the_token", func(t *testing.T) {
		a := appFor(t, enforce)
		statuses, _ := burst(t, a.statURL("v3", newMAC()), withRate(t, "rate-override-caller", "1000/1m"), 3)
		assert.Equal(t, []int{404, 404, 429}, statuses, "the override is 2/1m")
	})

	t.Run("empty_override_exempts", func(t *testing.T) {
		a := appFor(t, enforce)
		statuses, _ := burst(t, a.statURL("v3", newMAC()), withRate(t, "rate-exempt-caller", "1/1m"), 5)
		assert.Equal(t, []int{404, 404, 404, 404, 404}, statuses)
	})

	t.Run("defaults_hold_a_caller_without_a_rate", func(t *testing.T) {
		a := appFor(t, rateDefaults)
		statuses, _ := burst(t, a.statURL("v3", newMAC()), withRate(t, "rate-"+randomHex(4)), 4)
		assert.Equal(t, []int{404, 404, 404, 429}, statuses, "the default is 3/1m")
	})

	t.Run("a_token_rate_replaces_the_defaults", func(t *testing.T) {
		a := appFor(t, rateDefaults)
		statuses, _ := burst(t, a.statURL("v3", newMAC()), withRate(t, "rate-"+randomHex(4), "10/1m"), 5)
		assert.Equal(t, []int{404, 404, 404, 404, 404}, statuses)
	})

	t.Run("a_malformed_rate_is_not_rateless", func(t *testing.T) {
		a := appFor(t, rateDefaults)
		resp := get(t, a.statURL("v3", newMAC()), withRate(t, "rate-"+randomHex(4), "nope"))
		assert.Equal(t, http.StatusForbidden, resp.Status, "held to zero, not to the defaults, body: %s", resp.Body)
	})
}

// TestRatePermissiveModes: permissive rejects nothing; with warnings the
// caller is told; every finding is counted.
func TestRatePermissiveModes(t *testing.T) {
	for _, tc := range []struct {
		profile profile
		warns   bool
	}{{warn, true}, {silent, false}} {
		t.Run(tc.profile.name, func(t *testing.T) {
			a := appFor(t, tc.profile)
			url := a.statURL("v3", newMAC())
			warningName := warningMetric

			t.Run("over_the_rate", func(t *testing.T) {
				sub := "rate-" + tc.profile.name + "-" + randomHex(4)
				var last apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "rate", "reason": "rate_exceeded", "clientid": sub}, func() {
					_, last = burst(t, url, withRate(t, sub, "1/1m"), 2)
				})
				assert.Equal(t, http.StatusNotFound, last.Status, "permissive lets the request through")
				if tc.warns {
					assertWarning(t, last, "would-reject", "kind=rate", "reason=rate-exceeded", `limit="1/1m"`)
				} else {
					assert.Empty(t, warnings(last))
				}
				assert.Equal(t, 1.0, counted)
			})

			t.Run("malformed", func(t *testing.T) {
				sub := "rate-" + tc.profile.name + "-" + randomHex(4)
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "rate", "reason": "malformed", "clientid": sub}, func() {
					resp = get(t, url, withRate(t, sub, "100/1d"))
				})
				assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
				if tc.warns {
					assertWarning(t, resp, "malformed", "kind=rate")
				} else {
					assert.Empty(t, warnings(resp))
				}
				assert.Equal(t, 1.0, counted)
			})
		})
	}
}

// TestRateRejectionsAreCounted: a rate refusal is counted with its reason.
func TestRateRejectionsAreCounted(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())
	name := checkMetric

	t.Run("rate_exceeded", func(t *testing.T) {
		sub := "rate-count-" + randomHex(4)
		delta := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": "rate_exceeded", "clientid": sub}, func() {
			statuses, _ := burst(t, url, withRate(t, sub, "1/1m"), 2)
			assert.Equal(t, []int{404, 429}, statuses)
		})
		assert.Equal(t, 1.0, delta)
	})

	t.Run("rate_denied", func(t *testing.T) {
		sub := "rate-count-" + randomHex(4)
		delta := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": "rate_denied", "clientid": sub}, func() {
			assert.Equal(t, http.StatusForbidden, get(t, url, withRate(t, sub, "100/1d")).Status)
		})
		assert.Equal(t, 1.0, delta)
	})
}

// ---------------------------------------------------------------------------
// Parameter
// ---------------------------------------------------------------------------

// withParam mints a token for every endpoint that also carries parameter
// capabilities.
func withParam(t *testing.T, sub string, params ...string) string {
	t.Helper()
	caps := api(allCapability)
	for _, p := range params {
		caps = append(caps, paramPrefix+p)
	}

	return signer.mint(t, claims{Subject: sub, Capabilities: caps})
}

// paramRequest is one device request naming parameters.
type paramRequest struct {
	method string
	url    string
	body   string
}

// paramRequests builds the device requests for a connected device: what
// each one reads or writes is in the name.
func paramRequests(a *app, id string) map[string]paramRequest {
	config := a.configURL("v3", id)
	set := func(name string) string {
		return `{"parameters":[{"name":"` + name + `","value":"x","dataType":0}]}`
	}

	return map[string]paramRequest{
		"get_deviceinfo":            {http.MethodGet, config + "?names=Device.DeviceInfo.Manufacturer", ""},
		"get_deviceinfo_attributes": {http.MethodGet, config + "?names=Device.DeviceInfo.Manufacturer&attributes=notify", ""},
		"get_wifi":                  {http.MethodGet, config + "?names=Device.WiFi.Radio.1.Channel", ""},
		"get_both":                  {http.MethodGet, config + "?names=Device.DeviceInfo.Manufacturer,Device.WiFi.Radio.1.Channel", ""},
		"get_partial_deviceinfo":    {http.MethodGet, config + "?names=Device.DeviceInfo.", ""},
		"get_partial_device":        {http.MethodGet, config + "?names=Device.", ""},
		"set_deviceinfo":            {http.MethodPatch, config, set("Device.DeviceInfo.FriendlyName")},
		"set_deviceinfo_attributes": {http.MethodPatch, config, `{"parameters":[{"name":"Device.DeviceInfo.FriendlyName","attributes":{"notify":1}}]}`},
		"set_wifi":                  {http.MethodPatch, config, set("Device.WiFi.Radio.1.Channel")},
		"test_and_set_deviceinfo":   {http.MethodPatch, config, set("Device.DeviceInfo.FriendlyName")},
		"add_row_deviceinfo":        {http.MethodPost, a.tableURL("v3", id, "Device.DeviceInfo.Table."), `{"a":"b"}`},
		"replace_rows_deviceinfo":   {http.MethodPut, a.tableURL("v3", id, "Device.DeviceInfo.Table."), `{"0":{"a":"b"}}`},
		"delete_row_deviceinfo":     {http.MethodDelete, a.tableURL("v3", id, "Device.DeviceInfo.Table.1"), ""},
		"add_row_wifi":              {http.MethodPost, a.tableURL("v3", id, "Device.WiFi.Table."), `{"a":"b"}`},
	}
}

// TestParamEnforce: in enforce mode every parameter a request names must
// match a capability whose operation allows it.
func TestParamEnforce(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)
	reqs := paramRequests(a, dev.id)

	run := func(t *testing.T, tok string, name string) apiResponse {
		t.Helper()
		r := reqs[name]
		headers := map[string]string{}
		if strings.HasPrefix(name, "test_and_set") {
			headers["X-Webpa-Sync-New-Cid"] = "new"
		}
		resp := send(t, request{Method: r.method, URL: r.url, Token: tok, Headers: headers, Body: r.body})
		if resp.Status == http.StatusOK {
			dev.next(t)
		}

		return resp
	}

	cases := []struct {
		name    string
		params  []string
		allowed []string
		refused []string
	}{
		{
			name:    "read_deviceinfo",
			params:  []string{"Device.DeviceInfo.**:read"},
			allowed: []string{"get_deviceinfo", "get_deviceinfo_attributes", "get_partial_deviceinfo"},
			refused: []string{"get_wifi", "get_both", "get_partial_device", "set_deviceinfo", "set_deviceinfo_attributes", "test_and_set_deviceinfo", "add_row_deviceinfo", "replace_rows_deviceinfo", "delete_row_deviceinfo"},
		},
		{
			name:    "write_deviceinfo",
			params:  []string{"Device.DeviceInfo.**:write"},
			allowed: []string{"set_deviceinfo", "set_deviceinfo_attributes", "test_and_set_deviceinfo", "add_row_deviceinfo", "replace_rows_deviceinfo", "delete_row_deviceinfo"},
			refused: []string{"get_deviceinfo", "get_deviceinfo_attributes", "set_wifi", "add_row_wifi"},
		},
		{
			name:    "all_deviceinfo_by_default",
			params:  []string{"Device.DeviceInfo.**"},
			allowed: []string{"get_deviceinfo", "set_deviceinfo", "add_row_deviceinfo"},
			refused: []string{"get_wifi", "set_wifi"},
		},
		{
			name:    "all_deviceinfo_explicit",
			params:  []string{"Device.DeviceInfo.**:all"},
			allowed: []string{"get_deviceinfo", "set_deviceinfo"},
			refused: []string{"get_wifi"},
		},
		{
			name:    "two_capabilities_combine",
			params:  []string{"Device.DeviceInfo.**:read", "Device.WiFi.**:read"},
			allowed: []string{"get_deviceinfo", "get_wifi", "get_both"},
			refused: []string{"set_deviceinfo", "set_wifi"},
		},
		{
			name:    "single_level_wildcard",
			params:  []string{"Device.DeviceInfo.*:read"},
			allowed: []string{"get_deviceinfo"},
			refused: []string{"get_partial_deviceinfo", "get_wifi"},
		},
		{
			name:    "everything",
			params:  []string{"Device.**"},
			allowed: []string{"get_deviceinfo", "get_wifi", "get_partial_device", "set_wifi", "add_row_wifi"},
		},
		{
			name:    "pattern_is_case_insensitive",
			params:  []string{"device.deviceinfo.**:read"},
			allowed: []string{"get_deviceinfo"},
			refused: []string{"set_deviceinfo"},
		},
		{
			name:    "no_param_capability_is_unrestricted",
			allowed: []string{"get_deviceinfo", "get_wifi", "set_wifi", "add_row_wifi", "get_partial_device"},
		},
		{
			name:    "malformed_matches_nothing",
			params:  []string{"Device.**:wirte"},
			refused: []string{"get_deviceinfo", "set_deviceinfo"},
		},
		{
			name:    "malformed_beside_a_good_one",
			params:  []string{"Device.**:wirte", "Device.DeviceInfo.**:read"},
			allowed: []string{"get_deviceinfo"},
			refused: []string{"get_wifi"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tok := withParam(t, "", tc.params...)
			for _, name := range tc.allowed {
				resp := run(t, tok, name)
				assert.Equal(t, http.StatusOK, resp.Status, "%s should be allowed, body: %s, warnings: %v", name, resp.Body, warnings(resp))
			}
			for _, name := range tc.refused {
				resp := run(t, tok, name)
				assert.Equal(t, http.StatusForbidden, resp.Status, "%s should be refused, body: %s", name, resp.Body)
				assertWarning(t, resp, "rejected", "kind=param", "reason=parameter-not-allowed")
				dev.none(t)
			}
		})
	}

	t.Run("refusal_names_the_operation_and_parameter", func(t *testing.T) {
		tok := withParam(t, "", "Device.DeviceInfo.**:read")
		resp := run(t, tok, "get_wifi")
		assertWarning(t, resp, "rejected", "kind=param", "reason=parameter-not-allowed", "op=read", "name=Device.WiFi.Radio.1.Channel")
		resp = run(t, tok, "set_deviceinfo")
		assertWarning(t, resp, "rejected", "kind=param", "op=write", "name=Device.DeviceInfo.FriendlyName")
	})

	t.Run("malformed_is_reported", func(t *testing.T) {
		resp := run(t, withParam(t, "", "Device.**:wirte"), "get_deviceinfo")
		assertWarning(t, resp, "malformed", "kind=param", "Device.**:wirte")
	})

	t.Run("does_not_govern_webhooks_or_stat", func(t *testing.T) {
		tok := withParam(t, "", "Device.Nothing.**:read")
		assert.Equal(t, http.StatusOK, get(t, a.statURL("v3", dev.id), tok).Status)
		assert.Equal(t, http.StatusOK, get(t, a.hooksURL("v3"), tok).Status)
	})
}

// TestParamOverrides: an override replaces the token's parameter
// capabilities and an empty one exempts the caller.
func TestParamOverrides(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)
	reqs := paramRequests(a, dev.id)

	t.Run("override_narrows_a_permissive_token", func(t *testing.T) {
		tok := withParam(t, "param-override-caller", "Device.**")
		r := reqs["get_deviceinfo"]
		assert.Equal(t, http.StatusOK, send(t, request{Method: r.method, URL: r.url, Token: tok}).Status)
		dev.next(t)
		r = reqs["get_wifi"]
		assert.Equal(t, http.StatusForbidden, send(t, request{Method: r.method, URL: r.url, Token: tok}).Status, "the override is DeviceInfo read only")
		r = reqs["set_deviceinfo"]
		assert.Equal(t, http.StatusForbidden, send(t, request{Method: r.method, URL: r.url, Token: tok, Body: r.body}).Status)
	})

	t.Run("empty_override_exempts", func(t *testing.T) {
		tok := withParam(t, "param-exempt-caller", "Device.Nothing.**:read")
		r := reqs["set_wifi"]
		assert.Equal(t, http.StatusOK, send(t, request{Method: r.method, URL: r.url, Token: tok, Body: r.body}).Status)
		dev.next(t)
	})
}

// TestParamPermissiveModes: permissive refuses nothing; with warnings the
// caller is told what enforce would refuse; findings are counted.
func TestParamPermissiveModes(t *testing.T) {
	for _, tc := range []struct {
		profile profile
		warns   bool
	}{{warn, true}, {silent, false}} {
		t.Run(tc.profile.name, func(t *testing.T) {
			a := appFor(t, tc.profile)
			dev := xmidt.connect(t, okReply)
			reqs := paramRequests(a, dev.id)
			warningName := warningMetric
			sub := "param-" + tc.profile.name + "-" + randomHex(4)

			t.Run("not_allowed", func(t *testing.T) {
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "param", "reason": "parameter_not_allowed", "clientid": sub}, func() {
					r := reqs["get_wifi"]
					resp = send(t, request{Method: r.method, URL: r.url, Token: withParam(t, sub, "Device.DeviceInfo.**:read")})
				})
				assert.Equal(t, http.StatusOK, resp.Status, "permissive lets the request through, body: %s", resp.Body)
				dev.next(t)
				if tc.warns {
					assertWarning(t, resp, "would-reject", "kind=param", "reason=parameter-not-allowed", "op=read", "name=Device.WiFi.Radio.1.Channel")
				} else {
					assert.Empty(t, warnings(resp))
				}
				assert.Equal(t, 1.0, counted)
			})

			t.Run("malformed", func(t *testing.T) {
				var resp apiResponse
				counted := counterDelta(t, a, warningName, map[string]string{"kind": "param", "reason": "malformed", "clientid": sub}, func() {
					r := reqs["get_deviceinfo"]
					resp = send(t, request{Method: r.method, URL: r.url, Token: withParam(t, sub, "Device.**:wirte")})
				})
				assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
				dev.next(t)
				if tc.warns {
					assertWarning(t, resp, "malformed", "kind=param")
				} else {
					assert.Empty(t, warnings(resp))
				}
				assert.Equal(t, 1.0, counted)
			})
		})
	}
}

// TestParamRejectionsAreCounted: a parameter refusal is counted.
func TestParamRejectionsAreCounted(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)
	name := checkMetric
	sub := "param-count-" + randomHex(4)

	delta := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": "parameter_not_allowed", "clientid": sub}, func() {
		r := paramRequests(a, dev.id)["get_wifi"]
		assert.Equal(t, http.StatusForbidden, send(t, request{Method: r.method, URL: r.url, Token: withParam(t, sub, "Device.DeviceInfo.**:read")}).Status)
	})
	assert.Equal(t, 1.0, delta)
}

// ---------------------------------------------------------------------------
// Parameter capability age limit
// ---------------------------------------------------------------------------

// TestParamMaxAge: a token carrying a capability with an age limit is
// refused on every route once it is older than the limit, and a token
// without an iat cannot carry one at all.
func TestParamMaxAge(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)
	limited := paramPrefix + "Device.Secret.**"

	t.Run("young_token_is_fine", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: append(api(allCapability), limited), IssuedAt: time.Now().Add(-time.Minute)})
		resp := get(t, a.statURL("v3", dev.id), tok)
		assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Empty(t, warnings(resp))
	})

	t.Run("old_token_is_refused_everywhere", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: append(api(allCapability), limited), IssuedAt: time.Now().Add(-2 * time.Hour)})
		for _, endpoint := range capabilityEndpoints(t, "v3", dev.id) {
			resp := send(t, request{Method: endpoint.method, URL: a.primary + endpoint.path, Token: tok, Body: endpoint.body})
			assert.Equal(t, http.StatusForbidden, resp.Status, "%s: body: %s", endpoint.name, resp.Body)
			assertWarning(t, resp, "rejected", "kind=param", "reason=token-too-old", `cap="`+limited+`"`, "maxAge=30m0s", "age=")
		}
		dev.none(t)
	})

	t.Run("limit_with_operation_applies_to_that_operation", func(t *testing.T) {
		// the limit is on Device.Secret.** with any operation, so :read is covered
		tok := signer.mint(t, claims{Capabilities: append(api(allCapability), limited+":read"), IssuedAt: time.Now().Add(-2 * time.Hour)})
		resp := get(t, a.statURL("v3", dev.id), tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("other_capabilities_have_no_limit", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: append(api(allCapability), paramPrefix+"Device.DeviceInfo.**"), IssuedAt: time.Now().Add(-2 * time.Hour)})
		resp := get(t, a.statURL("v3", dev.id), tok)
		assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	})

	t.Run("no_issued_at_is_refused", func(t *testing.T) {
		tok := signer.mint(t, claims{Capabilities: append(api(allCapability), limited), noIssuedAt: true})
		resp := get(t, a.statURL("v3", dev.id), tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
		assertWarning(t, resp, "rejected", "kind=param", "reason=no-issued-at")
	})

	t.Run("override_does_not_exempt", func(t *testing.T) {
		tok := signer.mint(t, claims{Subject: "param-exempt-caller", Capabilities: append(api(allCapability), limited), IssuedAt: time.Now().Add(-2 * time.Hour)})
		resp := get(t, a.statURL("v3", dev.id), tok)
		assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
	})

	t.Run("permissive_warns_instead", func(t *testing.T) {
		w := appFor(t, warn)
		sub := "age-" + randomHex(4)
		tok := signer.mint(t, claims{Subject: sub, Capabilities: append(api(allCapability), limited), IssuedAt: time.Now().Add(-2 * time.Hour)})
		warningName := warningMetric
		var resp apiResponse
		counted := counterDelta(t, w, warningName, map[string]string{"kind": "param", "reason": "token_too_old", "clientid": sub}, func() {
			resp = get(t, w.statURL("v3", newMAC()), tok)
		})
		assert.Equal(t, http.StatusNotFound, resp.Status, "body: %s", resp.Body)
		assertWarning(t, resp, "would-reject", "kind=param", "reason=token-too-old")
		assert.Equal(t, 1.0, counted)
	})

	t.Run("refusal_is_counted", func(t *testing.T) {
		sub := "age-" + randomHex(4)
		tok := signer.mint(t, claims{Subject: sub, Capabilities: append(api(allCapability), limited), IssuedAt: time.Now().Add(-2 * time.Hour)})
		name := checkMetric
		delta := counterDelta(t, a, name, map[string]string{"outcome": "rejected", "reason": "token_too_old", "clientid": sub}, func() {
			assert.Equal(t, http.StatusForbidden, get(t, a.statURL("v3", dev.id), tok).Status)
		})
		assert.Equal(t, 1.0, delta)
	})
}

// TestKindsAreIndependent: each kind is checked on its own; passing one
// never excuses failing another, and a request must pass every kind the
// token carries.
func TestKindsAreIndependent(t *testing.T) {
	a := appFor(t, enforce)
	dev := xmidt.connect(t, okReply)
	url := a.configURL("v3", dev.id) + "?names=Device.DeviceInfo.Manufacturer"

	all := signer.mint(t, claims{Capabilities: []string{
		apiPrefix + allCapability,
		cidrPrefix + includedNetwork,
		ratePrefix + "100/1m",
		paramPrefix + "Device.DeviceInfo.**:read",
	}})
	resp := get(t, url, all)
	assert.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	dev.next(t)

	for name, tok := range map[string]string{
		"bad_cidr":     signer.mint(t, claims{Capabilities: []string{apiPrefix + allCapability, cidrPrefix + excludedNetwork, ratePrefix + "100/1m", paramPrefix + "Device.**"}}),
		"bad_rate":     signer.mint(t, claims{Capabilities: []string{apiPrefix + allCapability, cidrPrefix + includedNetwork, ratePrefix + "100/1d", paramPrefix + "Device.**"}}),
		"bad_param":    signer.mint(t, claims{Capabilities: []string{apiPrefix + allCapability, cidrPrefix + includedNetwork, ratePrefix + "100/1m", paramPrefix + "Device.WiFi.**"}}),
		"bad_endpoint": signer.mint(t, claims{Capabilities: []string{apiPrefix + hooksGetCapability, cidrPrefix + includedNetwork, ratePrefix + "100/1m", paramPrefix + "Device.**"}}),
	} {
		t.Run(name, func(t *testing.T) {
			resp := get(t, url, tok)
			assert.Equal(t, http.StatusForbidden, resp.Status, "body: %s", resp.Body)
			dev.none(t)
		})
	}
}
