// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Webhooks.
//
// POST /api/v2/hook goes through a compatibility shim that repairs a missing
// or invalid duration/until, adds an X-Xmidt-Warning header and registers
// the webhook anyway.  POST /api/v3/hook applies the rules strictly and
// rejects such registrations.  GET /hooks lists what the caller may see,
// with secrets obfuscated.
// ---------------------------------------------------------------------------

// register posts a registration and returns the response.
func register(t *testing.T, a *app, version, tok, body string) apiResponse {
	t.Helper()
	return send(t, request{Method: http.MethodPost, URL: a.hookURL(version), Token: tok, Body: body})
}

// TestWebhookRegisterAndList: a registration on either version appears in
// GET /hooks on either version, with the secret obfuscated and the rest as
// registered.
func TestWebhookRegisterAndList(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	for _, version := range []string{"v2", "v3"} {
		t.Run(version, func(t *testing.T) {
			hookURL := uniqueListenerURL()
			mac := newMAC()
			secret := "sec-" + randomHex(8)
			body := hookBody(t, version, map[string]any{
				"config":      map[string]any{"url": hookURL, "content_type": "application/json", "secret": secret},
				"matcher":     map[string]any{"device_id": []string{mac}},
				"events":      []string{"compliance-test"},
				"failure_url": listenerURL + "/failures",
			})

			resp := register(t, a, version, tok, body)
			require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
			assert.Equal(t, "Success", resp.message())
			assert.NotContains(t, resp.Body, secret, "the registration response must not echo the secret")

			for _, listVersion := range []string{"v2", "v3"} {
				list := send(t, request{Method: http.MethodGet, URL: a.hooksURL(listVersion), Token: tok})
				require.Equal(t, http.StatusOK, list.Status, "body: %s", list.Body)
				assert.NotContains(t, list.Body, secret, "the plaintext secret must not appear anywhere in GET /hooks")

				var hooks []listedHook
				require.NoError(t, json.Unmarshal([]byte(list.Body), &hooks), "body: %s", list.Body)
				var found *listedHook
				for i := range hooks {
					if hooks[i].Config.URL == hookURL {
						found = &hooks[i]
					}
				}
				require.NotNil(t, found, "the registration should appear in GET /%s/hooks", listVersion)
				assert.Equal(t, "<obfuscated>", found.Config.Secret, "the secret must be obfuscated")
				assert.Equal(t, []string{mac}, found.Matcher.DeviceID)
				assert.Equal(t, []string{"compliance-test"}, found.Events)
				assert.Equal(t, listenerURL+"/failures", found.FailureURL)
				// Both registrations last a minute.  v2 lists the duration as
				// it always has, an integer of nanoseconds; v3 as a string.
				if listVersion == "v2" {
					assert.Equal(t, "60000000000", string(found.Duration), "v2 lists the duration as nanoseconds")
				} else {
					assert.Equal(t, `"1m0s"`, string(found.Duration), "v3 lists the duration as a string")
				}
			}
		})
	}
}

// TestWebhookListIsAJSONArray: with nothing registered the list is an empty
// JSON array, not null.
func TestWebhookListIsAJSONArray(t *testing.T) {
	a := appFor(t, enforce)
	argus.reset()

	resp := get(t, a.hooksURL("v3"), allToken(t))
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"))
	assert.JSONEq(t, "[]", resp.Body)
}

// TestWebhookV2Compatibility: the v2 shim's repairs and warnings.
func TestWebhookV2Compatibility(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	// a numeric v2 duration is in seconds; the configured maximum is 5m
	tooLongSeconds := int64(10000 * 3600)

	t.Run("valid_duration_has_no_warning", func(t *testing.T) {
		resp := register(t, a, "v2", tok, hookBody(t, "v2", map[string]any{"duration": int64(30)}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Empty(t, resp.Header.Values(v2WarningHeader))
	})

	t.Run("missing_duration_and_until_is_defaulted", func(t *testing.T) {
		resp := register(t, a, "v2", tok, hookBody(t, "v2", map[string]any{"duration": nil}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Contains(t, resp.Header.Get(v2WarningHeader), "Unset duration and until fields will not be accepted in v3")
	})

	t.Run("duration_too_long_is_defaulted", func(t *testing.T) {
		resp := register(t, a, "v2", tok, hookBody(t, "v2", map[string]any{"duration": tooLongSeconds}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Contains(t, resp.Header.Get(v2WarningHeader), "Invalid duration will not be accepted in v3:")
	})

	t.Run("until_too_far_is_defaulted", func(t *testing.T) {
		until := time.Now().Add(10 * 365 * 24 * time.Hour).UTC().Format(time.RFC3339)
		resp := register(t, a, "v2", tok, hookBody(t, "v2", map[string]any{"duration": nil, "until": until}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Contains(t, resp.Header.Get(v2WarningHeader), "Invalid until value will not be accepted in v3:")
	})

	t.Run("wrong_field_type_is_rejected", func(t *testing.T) {
		resp := register(t, a, "v2", tok, hookBody(t, "v2", map[string]any{"events": "compliance-test"}))
		assert.Equal(t, http.StatusBadRequest, resp.Status, "body: %s", resp.Body)
		assert.Contains(t, resp.Body, "must be of type")
	})

	t.Run("malformed_json_is_rejected", func(t *testing.T) {
		resp := register(t, a, "v2", tok, "{bad")
		assert.Equal(t, http.StatusBadRequest, resp.Status, "body: %s", resp.Body)
		assert.Contains(t, resp.Body, "failed to JSON unmarshal webhook:")
	})
}

// TestWebhookV3Validation: v3 rejects what v2 repairs, plus the other
// registration rules.
func TestWebhookV3Validation(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	future := time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339)

	rejected := []struct {
		name      string
		overrides map[string]any
		raw       string
	}{
		{name: "no_duration_or_until", overrides: map[string]any{"duration": nil}},
		{name: "duration_too_long", overrides: map[string]any{"duration": "10000h"}},
		{name: "duration_and_until", overrides: map[string]any{"duration": "30s", "until": future}},
		{name: "until_in_the_past", overrides: map[string]any{"duration": nil, "until": "2000-01-01T00:00:00Z"}},
		{name: "until_too_far", overrides: map[string]any{"duration": nil, "until": time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)}},
		{name: "zero_events", overrides: map[string]any{"events": []string{}}},
		{name: "no_events", overrides: map[string]any{"events": nil}},
		{name: "invalid_event_regex", overrides: map[string]any{"events": []string{"("}}},
		{name: "invalid_device_regex", overrides: map[string]any{"matcher": map[string]any{"device_id": []string{"("}}}},
		{name: "invalid_failure_url", overrides: map[string]any{"failure_url": "not-a-url"}},
		{name: "invalid_config_url", overrides: map[string]any{"config": map[string]any{"url": "not-a-url", "content_type": "application/json"}}},
		{name: "no_config_url", overrides: map[string]any{"config": map[string]any{"content_type": "application/json"}}},
		{name: "wrong_field_type", overrides: map[string]any{"events": "compliance-test"}},
		{name: "malformed_json", raw: "{bad"},
		{name: "empty_object", raw: "{}"},
	}

	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			body := tc.raw
			if body == "" {
				body = hookBody(t, "v3", tc.overrides)
			}
			resp := register(t, a, "v3", tok, body)
			assert.Equal(t, http.StatusBadRequest, resp.Status, "body: %s", resp.Body)
			assert.True(t, strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json"), "Content-Type: %q", resp.Header.Get("Content-Type"))
			assert.NotEmpty(t, resp.message(), "the body should say what was wrong: %s", resp.Body)
			assertNoLeak(t, resp.Body)
		})
	}

	t.Run("valid_registration", func(t *testing.T) {
		resp := register(t, a, "v3", tok, hookBody(t, "v3", map[string]any{"duration": "30s"}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Equal(t, "Success", resp.message())
		assert.Empty(t, resp.Header.Values(v2WarningHeader), "v3 never emits the v2 warning")
	})

	t.Run("valid_until", func(t *testing.T) {
		resp := register(t, a, "v3", tok, hookBody(t, "v3", map[string]any{"duration": nil, "until": future}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	})
}

// TestWebhookV3RegistrationV2: v3 also takes the v2 registration schema,
// which has no events, device IDs or until, and is checked by the rules
// that apply to its shape.  A body is read as that schema when it has a
// field only it has, such as canonical_name or webhooks.
func TestWebhookV3RegistrationV2(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	body := func(t *testing.T, overrides map[string]any) string {
		t.Helper()
		b := map[string]any{
			"canonical_name": "compliance-" + randomHex(6),
			"webhooks": []map[string]any{{
				"accept":        "application/json",
				"secret":        "compliance-secret",
				"receiver_urls": []string{uniqueListenerURL()},
			}},
			"matcher": []map[string]any{{"field": "destination", "regex": "compliance-test"}},
			"expires": time.Now().Add(time.Minute).UTC().Format(time.RFC3339),
		}
		for k, v := range overrides {
			if v == nil {
				delete(b, k)
				continue
			}
			b[k] = v
		}
		data, err := json.Marshal(b)
		require.NoError(t, err)
		return string(data)
	}

	t.Run("valid_without_matcher", func(t *testing.T) {
		resp := register(t, a, "v3", tok, body(t, map[string]any{"matcher": nil}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Equal(t, "Success", resp.message())
	})

	t.Run("valid_registration", func(t *testing.T) {
		name := "compliance-" + randomHex(6)
		resp := register(t, a, "v3", tok, body(t, map[string]any{"canonical_name": name}))
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		assert.Equal(t, "Success", resp.message())

		// It is listed in its own schema, with the secret obfuscated.
		type listedV2 struct {
			CanonicalName string `json:"canonical_name"`
			Webhooks      []struct {
				Secret       string   `json:"secret"`
				ReceiverURLs []string `json:"receiver_urls"`
			} `json:"webhooks"`
		}
		var listed *listedV2
		for _, raw := range listRawHooks(t, a, tok, nil) {
			if !v2schema(raw) {
				continue
			}
			var entry listedV2
			require.NoError(t, json.Unmarshal(raw, &entry), "GET /hooks entry: %s", raw)
			if entry.CanonicalName == name {
				listed = &entry
			}
		}
		require.NotNil(t, listed, "the registration should be listed by its canonical name")
		require.Len(t, listed.Webhooks, 1)
		assert.Equal(t, "<obfuscated>", listed.Webhooks[0].Secret)
		assert.Len(t, listed.Webhooks[0].ReceiverURLs, 1)
	})

	rejected := []struct {
		name      string
		overrides map[string]any
	}{
		{name: "expired", overrides: map[string]any{"expires": "2000-01-01T00:00:00Z"}},
		{name: "no_expiry", overrides: map[string]any{"expires": nil}},
		{name: "invalid_receiver_url", overrides: map[string]any{"webhooks": []map[string]any{{"receiver_urls": []string{"not-a-url"}}}}},
		{name: "no_receiver_url", overrides: map[string]any{"webhooks": []map[string]any{{"accept": "application/json"}}}},
		{name: "invalid_failure_url", overrides: map[string]any{"failure_url": "not-a-url"}},
		{name: "invalid_matcher_regex", overrides: map[string]any{"matcher": []map[string]any{{"field": "source", "regex": "("}}}},
		{name: "wrong_field_type", overrides: map[string]any{"webhooks": "not-a-list"}},
	}

	for _, tc := range rejected {
		t.Run(tc.name, func(t *testing.T) {
			resp := register(t, a, "v3", tok, body(t, tc.overrides))
			assert.Equal(t, http.StatusBadRequest, resp.Status, "body: %s", resp.Body)
			assert.NotEmpty(t, resp.message(), "the body should say what was wrong: %s", resp.Body)
			assertNoLeak(t, resp.Body)
		})
	}
}

// TestWebhookLifecycle: defaults, overwrite-by-URL and expiry.
func TestWebhookLifecycle(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)

	t.Run("matcher_defaults_to_match_all", func(t *testing.T) {
		hookURL := uniqueListenerURL()
		body := hookBody(t, "v3", map[string]any{
			"config":  map[string]any{"url": hookURL, "content_type": "application/json"},
			"matcher": nil,
		})
		resp := register(t, a, "v3", tok, body)
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

		hook := findHook(t, a, tok, hookURL)
		require.NotNil(t, hook, "the registration should be listed")
		assert.Equal(t, []string{".*"}, hook.Matcher.DeviceID)
	})

	t.Run("same_url_overwrites", func(t *testing.T) {
		hookURL := uniqueListenerURL()
		for _, event := range []string{"first-event", "second-event"} {
			body := hookBody(t, "v3", map[string]any{
				"config": map[string]any{"url": hookURL, "content_type": "application/json"},
				"events": []string{event},
			})
			resp := register(t, a, "v3", tok, body)
			require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		}

		var matching int
		for _, h := range listHooks(t, a, tok, nil) {
			if h.Config.URL == hookURL {
				matching++
				assert.Equal(t, []string{"second-event"}, h.Events, "the second registration replaces the first")
			}
		}
		assert.Equal(t, 1, matching, "one registration per URL")
	})

	t.Run("expires", func(t *testing.T) {
		hookURL := uniqueListenerURL()
		// two seconds: the store keeps a TTL in whole seconds, and one of
		// zero means no expiry at all
		body := hookBody(t, "v3", map[string]any{
			"config":   map[string]any{"url": hookURL, "content_type": "application/json"},
			"duration": "2s",
		})
		resp := register(t, a, "v3", tok, body)
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
		require.NotNil(t, findHook(t, a, tok, hookURL), "the registration should be listed before it expires")

		require.Eventually(t, func() bool { return findHook(t, a, tok, hookURL) == nil },
			5*time.Second, 100*time.Millisecond, "an expired webhook should disappear from GET /hooks")
	})
}

// TestWebhookListCrossTenantIsolation: a caller listing webhooks sees only
// those registered under its own partners, and a partner header cannot
// widen that.
func TestWebhookListCrossTenantIsolation(t *testing.T) {
	a := appFor(t, enforce)
	tokenA := signer.mint(t, claims{Subject: "tenant-a", Partners: []string{"partner-a"}, Capabilities: api(allCapability)})
	tokenB := signer.mint(t, claims{Subject: "tenant-b", Partners: []string{"partner-b"}, Capabilities: api(allCapability)})
	tokenAB := signer.mint(t, claims{Subject: "tenant-ab", Partners: []string{"partner-a", "partner-b"}, Capabilities: api(allCapability)})

	urlA, urlB := uniqueListenerURL()+"&tenant=a", uniqueListenerURL()+"&tenant=b"
	for _, reg := range []struct{ tok, url string }{{tokenA, urlA}, {tokenB, urlB}} {
		body := hookBody(t, "v3", map[string]any{"config": map[string]any{"url": reg.url, "content_type": "application/json"}})
		resp := register(t, a, "v3", reg.tok, body)
		require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)
	}

	urlsA := hookURLs(listHooks(t, a, tokenA, nil))
	assert.Contains(t, urlsA, urlA, "A should see its own webhook")
	assert.NotContains(t, urlsA, urlB, "A must not see B's webhook")

	urlsB := hookURLs(listHooks(t, a, tokenB, nil))
	assert.Contains(t, urlsB, urlB, "B should see its own webhook")
	assert.NotContains(t, urlsB, urlA, "B must not see A's webhook")

	urlsAB := hookURLs(listHooks(t, a, tokenAB, nil))
	assert.Contains(t, urlsAB, urlA, "a caller for both partners sees A's webhook")
	assert.Contains(t, urlsAB, urlB, "a caller for both partners sees B's webhook")

	spoofed := hookURLs(listHooks(t, a, tokenA, map[string]string{"X-Xmidt-Partner-Id": "partner-b", "X-Xmidt-Partner-Ids": "partner-b"}))
	assert.NotContains(t, spoofed, urlB, "a spoofed partner header must not expose B's webhook to A")
}

// TestWebhookRegistrationRecordsPartners: the partner IDs a webhook is
// registered under are the token's, not a header's.
func TestWebhookRegistrationRecordsPartners(t *testing.T) {
	a := appFor(t, enforce)
	tok := signer.mint(t, claims{Subject: "tenant-c", Partners: []string{"partner-c"}, Capabilities: api(allCapability)})
	hookURL := uniqueListenerURL()
	body := hookBody(t, "v3", map[string]any{"config": map[string]any{"url": hookURL, "content_type": "application/json"}})
	resp := send(t, request{Method: http.MethodPost, URL: a.hookURL("v3"), Token: tok, Body: body,
		Headers: map[string]string{"X-Xmidt-Partner-Id": "partner-a", "X-Xmidt-Partner-Ids": "partner-a"}})
	require.Equal(t, http.StatusOK, resp.Status, "body: %s", resp.Body)

	partnerA := signer.mint(t, claims{Subject: "tenant-a", Partners: []string{"partner-a"}, Capabilities: api(allCapability)})
	assert.NotContains(t, hookURLs(listHooks(t, a, partnerA, nil)), hookURL, "the spoofed partner must not own the webhook")
	assert.Contains(t, hookURLs(listHooks(t, a, tok, nil)), hookURL, "the token's partner owns the webhook")
}

// TestWebhookErrorsDoNotLeak: validation errors from the webhook handlers do
// not expose internal service URLs or details.
func TestWebhookErrorsDoNotLeak(t *testing.T) {
	a := appFor(t, enforce)
	tok := allToken(t)
	bodies := map[string]string{
		"invalid_webhook_url": `{"config":{"url":"not-a-valid-url"}}`,
		"empty_registration":  `{}`,
		"wrong_field_type":    `{"events":"not-a-list"}`,
		"invalid_failure_url": fmt.Sprintf(`{"config":{"url":%q},"failure_url":"not-a-url","events":["x"],"duration":"1m"}`, listenerURL),
		"malformed_json":      "{invalid json",
	}

	for name, body := range bodies {
		for _, version := range []string{"v2", "v3"} {
			t.Run(name+"/"+version, func(t *testing.T) {
				resp := register(t, a, version, tok, body)
				assert.Equal(t, http.StatusBadRequest, resp.Status, "invalid registrations should be rejected with 400, body: %s", resp.Body)
				assertNoLeak(t, resp.Body)
			})
		}
	}
}
