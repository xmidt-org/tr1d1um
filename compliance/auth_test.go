// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"encoding/base64"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// ---------------------------------------------------------------------------
// Authentication.
//
// tr1d1um validates bearer tokens with bascule.  Authentication failures
// map as follows:
//
//	no Authorization header         → 401
//	unsupported scheme (e.g. Basic) → 401
//	JWT rejected (garbage, bad sig) → 401
//	malformed header                → 400 or 401
//	valid token, missing capability → 403 (see capability_matrix_test.go)
// ---------------------------------------------------------------------------

// authProbeURLs returns one URL per API family so authentication is
// exercised on every kind of route.
func authProbeURLs(a *app) map[string]string {
	id := newMAC()
	return map[string]string{
		"stat":   a.statURL("v3", id),
		"config": a.configURL("v3", id) + "?names=Device.DeviceInfo.Manufacturer",
		"hooks":  a.hooksURL("v3"),
	}
}

func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// TestAuthNoToken: a request with no Authorization header is rejected with
// 401 on every route family.
func TestAuthNoToken(t *testing.T) {
	a := appFor(t, enforce)
	for name, url := range authProbeURLs(a) {
		t.Run(name, func(t *testing.T) {
			resp := get(t, url, "")
			assert.Equal(t, http.StatusUnauthorized, resp.Status, "body: %s", resp.Body)
		})
	}
}

// TestAuthBadToken: unusable Authorization headers are rejected, and a
// header that cannot be parsed at all is a client error rather than a pass.
func TestAuthBadToken(t *testing.T) {
	a := appFor(t, enforce)
	badTokens := []struct {
		name   string
		header string
		want   []int
	}{
		{"garbage_bearer", "Bearer not.a.jwt", []int{http.StatusUnauthorized}},
		{"unsupported_scheme", "Token abcdef", []int{http.StatusUnauthorized}},
		{"basic_scheme", basic("foo", "bar"), []int{http.StatusUnauthorized}},
		{"scheme_without_value", "Bearer", []int{http.StatusBadRequest, http.StatusUnauthorized}},
		{"no_scheme", "some-random-string", []int{http.StatusBadRequest, http.StatusUnauthorized}},
		{"expired", signer.mint(t, claims{Capabilities: api(allCapability), Expiration: time.Now().Add(-time.Minute)}), []int{http.StatusUnauthorized}},
		{"not_yet_issued", signer.mint(t, claims{Capabilities: api(allCapability), IssuedAt: time.Now().Add(time.Hour)}), []int{http.StatusUnauthorized}},
	}

	for _, tc := range badTokens {
		for route, url := range authProbeURLs(a) {
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				resp := get(t, url, tc.header)
				assert.Contains(t, tc.want, resp.Status, "%q should be rejected with one of %v, body: %s", tc.header, tc.want, resp.Body)
			})
		}
	}
}

// forgedTokens builds tokens that carry fully-authorizing claims but cannot
// have a valid signature.  Each must be rejected as an authentication
// failure.
func forgedTokens(t *testing.T) map[string]string {
	t.Helper()

	genuine := jwtParts(t, allToken(t))
	forged := strings.Split(forgeUnsignedToken(t, forgedClaims()), ".")

	return map[string]string{
		"unknown_rs256_key":  forgeRS256Token(t, forgedClaims()),
		"alg_none":           forgeUnsignedToken(t, forgedClaims()),
		"tampered_payload":   genuine[0] + "." + forged[1] + "." + genuine[2],
		"stripped_signature": genuine[0] + "." + genuine[1] + ".",
		"unknown_key_id": func() string {
			other, err := newTokenSigner()
			if err != nil {
				t.Fatal(err)
			}
			other.keyID = "nobody"
			return strings.TrimPrefix(other.mint(t, claims{Capabilities: api(allCapability)}), "Bearer ")
		}(),
	}
}

// TestForgedJWTRejected: tokens with a missing, wrong or non-matching
// signature are rejected with 401 on every route family, even though their
// claims would grant full access.
func TestForgedJWTRejected(t *testing.T) {
	a := appFor(t, enforce)
	for name, tok := range forgedTokens(t) {
		for route, url := range authProbeURLs(a) {
			t.Run(name+"/"+route, func(t *testing.T) {
				resp := get(t, url, "Bearer "+tok)
				assert.Equal(t, http.StatusUnauthorized, resp.Status, "forged token must fail authentication, body: %s", resp.Body)
			})
		}
	}
}

// TestBasicAuthRejectedWhenJWTConfigured: with JWT authentication
// configured, Basic credentials of any shape are rejected with 401 on every
// route and method, with or without a spoofed partner header.
func TestBasicAuthRejectedWhenJWTConfigured(t *testing.T) {
	a := appFor(t, enforce)
	id := newMAC()
	registration := hookBody(t, "v3", nil)

	cases := []struct {
		name   string
		header string
		method string
		url    string
		body   string
	}{
		{"arbitrary_on_config", basic("foo", "bar"), http.MethodGet, a.configURL("v3", id) + "?names=Device.DeviceInfo.Manufacturer", ""},
		{"arbitrary_on_stat", basic("attacker", "password"), http.MethodGet, a.statURL("v3", id), ""},
		{"empty_password", basic("user", ""), http.MethodGet, a.configURL("v3", id), ""},
		{"empty_username", basic("", "password"), http.MethodGet, a.statURL("v3", id), ""},
		{"special_chars", basic("admin", "admin' OR '1'='1"), http.MethodGet, a.configURL("v3", id), ""},
		{"configured_credential_of_another_profile", "Basic " + basicCredential, http.MethodGet, a.statURL("v3", id), ""},
		{"invalid_base64", "Basic not-valid-base64!!!", http.MethodGet, a.statURL("v3", id), ""},
		{"hooks_list", basic("hacker", "hacker"), http.MethodGet, a.hooksURL("v3"), ""},
		{"hook_register_v2", basic("test", "test"), http.MethodPost, a.hookURL("v2"), hookBody(t, "v2", nil)},
		{"hook_register_v3", basic("test", "test"), http.MethodPost, a.hookURL("v3"), registration},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := send(t, request{Method: tc.method, URL: tc.url, Token: tc.header, Body: tc.body,
				Headers: map[string]string{"X-Xmidt-Partner-Id": "victim-partner"}})
			assert.Equal(t, http.StatusUnauthorized, resp.Status, "Basic auth must be rejected with 401, body: %s", resp.Body)
		})
	}
}

// TestAuthErrorsDoNotLeak: authentication failures do not echo the
// credential, do not say whether a user exists, and are the same for
// different user names.
func TestAuthErrorsDoNotLeak(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())

	cases := []struct {
		name   string
		header string
		secret string
		want   []int
	}{
		{"no_auth", "", "", []int{http.StatusUnauthorized}},
		{"invalid_bearer", "Bearer not.a.jwt", "not.a.jwt", []int{http.StatusUnauthorized}},
		{"invalid_basic", basic("user", "wrong-password"), "wrong-password", []int{http.StatusUnauthorized}},
		{"malformed_bearer", "Bearer", "", []int{http.StatusBadRequest, http.StatusUnauthorized}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := get(t, url, tc.header)
			assert.Contains(t, tc.want, resp.Status, "body: %s", resp.Body)
			if tc.secret != "" {
				assert.NotContains(t, resp.Body, tc.secret, "the response must not echo the supplied credential")
			}
			assertNoLeak(t, resp.Body)
			lower := strings.ToLower(resp.Body)
			for _, phrase := range []string{"user not found", "no such user", "unknown user", "invalid password", "wrong password", "incorrect password"} {
				assert.NotContains(t, lower, phrase, "auth errors must not distinguish a bad user from a bad password")
			}
		})
	}

	t.Run("username_does_not_change_response", func(t *testing.T) {
		first := get(t, url, basic("user", "wrong-password"))
		second := get(t, url, basic("administrator", "wrong-password"))
		assert.Equal(t, first.Status, second.Status)
		assert.Equal(t, first.Body, second.Body, "different user names must produce identical errors")
	})
}

// TestTokenWithoutPartners: a token that names no partner is refused as bad
// credentials, since tr1d1um could not say who the request acts for.
func TestTokenWithoutPartners(t *testing.T) {
	a := appFor(t, enforce)
	url := a.statURL("v3", newMAC())

	for name, c := range map[string]claims{
		"no_partner_claim": {Capabilities: api(allCapability), noPartners: true},
		"empty_partners":   {Capabilities: api(allCapability), Partners: []string{}},
	} {
		t.Run(name, func(t *testing.T) {
			resp := get(t, url, signer.mint(t, c))
			assert.Equal(t, http.StatusUnauthorized, resp.Status, "body: %s", resp.Body)
		})
	}
}

// leakPatterns are unambiguous indicators of an internal information leak:
// file paths, stack traces, panics, source references and internal
// addresses.  Their presence in an error body is always a defect.
var leakPatterns = []string{
	"/usr/", "/home/", "/var/", `C:\`,
	"goroutine", "panic:", "runtime.", ".go:",
	"http://localhost", "http://127.0.0.1",
	"http://talaria", "http://scytale", "http://petasos", "http://argus", "http://caduceus",
}

func assertNoLeak(t *testing.T, body string) {
	t.Helper()
	lower := strings.ToLower(body)
	for _, pattern := range leakPatterns {
		assert.NotContains(t, lower, strings.ToLower(pattern), "error response leaks internal detail %q (CWE-209). Body: %s", pattern, body)
	}
	assert.LessOrEqual(t, strings.Count(body, "\n"), 5, "response is unexpectedly long and multi-line, possibly a stack trace: %s", body)
}
