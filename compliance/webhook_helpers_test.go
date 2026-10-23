// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// listenerURL is where registered webhooks point.  Nothing listens there;
// the suite checks registration, not delivery, which is caduceus's job.
const listenerURL = "http://listener.example.test/events"

// uniqueListenerURL makes a listener URL unique to one registration, since
// webhooks are keyed by URL.
func uniqueListenerURL() string {
	return listenerURL + "?run=" + randomHex(6)
}

// hookBody builds a registration body for the API version.  A v2 duration
// is a number of seconds; a v3 duration is a Go duration string.  A nil
// override value removes that top-level key.
func hookBody(t *testing.T, version string, overrides map[string]any) string {
	t.Helper()
	body := map[string]any{
		"config":  map[string]any{"url": uniqueListenerURL(), "content_type": "application/json", "secret": "compliance-secret"},
		"matcher": map[string]any{"device_id": []string{newMAC()}},
		"events":  []string{"compliance-test"},
	}
	if version == "v2" {
		body["duration"] = int64(60)
	} else {
		body["duration"] = "1m"
	}
	for k, v := range overrides {
		if v == nil {
			delete(body, k)
			continue
		}
		body[k] = v
	}
	data, err := json.Marshal(body)
	require.NoError(t, err)

	return string(data)
}

// listedHook is the shape of one entry in GET /hooks.
type listedHook struct {
	Config struct {
		URL         string `json:"url"`
		ContentType string `json:"content_type"`
		Secret      string `json:"secret"`
	} `json:"config"`
	Matcher struct {
		DeviceID []string `json:"device_id"`
	} `json:"matcher"`
	Events     []string `json:"events"`
	FailureURL string   `json:"failure_url,omitempty"`
	// Duration is kept as sent: a number of nanoseconds on v2, a string on v3.
	Duration json.RawMessage `json:"duration"`
}

// listHooks returns every webhook the token may see in the v1 schema.  A
// registration in the v2 schema (a canonical_name and a list of webhooks)
// is listed alongside them and is left out here; TestWebhookV3RegistrationV2
// reads those.
func listHooks(t *testing.T, a *app, tok string, headers map[string]string) []listedHook {
	t.Helper()
	var hooks []listedHook
	for _, raw := range listRawHooks(t, a, tok, headers) {
		if v2schema(raw) {
			continue
		}
		var h listedHook
		require.NoError(t, json.Unmarshal(raw, &h), "GET /hooks entry: %s", raw)
		hooks = append(hooks, h)
	}

	return hooks
}

// listRawHooks returns every entry of GET /hooks as the server sent it.
func listRawHooks(t *testing.T, a *app, tok string, headers map[string]string) []json.RawMessage {
	t.Helper()
	resp := send(t, request{Method: "GET", URL: a.hooksURL("v3"), Token: tok, Headers: headers})
	require.Equal(t, 200, resp.Status, "GET /hooks body: %s", resp.Body)
	var entries []json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(resp.Body), &entries), "GET /hooks body: %s", resp.Body)

	return entries
}

// v2schema reports whether a listed entry is in the v2 registration schema.
func v2schema(raw json.RawMessage) bool {
	var probe struct {
		CanonicalName *string `json:"canonical_name"`
	}

	return json.Unmarshal(raw, &probe) == nil && probe.CanonicalName != nil
}

// findHook returns the listed webhook with the given URL, or nil.
func findHook(t *testing.T, a *app, tok, url string) *listedHook {
	t.Helper()
	for _, h := range listHooks(t, a, tok, nil) {
		if h.Config.URL == url {
			return &h
		}
	}

	return nil
}

// hookURLs returns the URLs of the listed webhooks.
func hookURLs(hooks []listedHook) []string {
	urls := make([]string, 0, len(hooks))
	for _, h := range hooks {
		urls = append(urls, h.Config.URL)
	}

	return urls
}
