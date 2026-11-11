// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/ancla"
	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/ancla/chrysom"
	"github.com/xmidt-org/ancla/model"
	"github.com/xmidt-org/ancla/schema"
	webhook "github.com/xmidt-org/webhook-schema"
)

// fakeStore is a chrysom.PushReader holding items by owner.
type fakeStore struct {
	items map[string]chrysom.Items
	err   error
}

func (f fakeStore) PushItem(context.Context, string, model.Item) (chrysom.PushResult, error) {
	return chrysom.CreatedPushResult, nil
}

func (f fakeStore) RemoveItem(context.Context, string, string) (model.Item, error) {
	return model.Item{}, nil
}

func (f fakeStore) GetItems(_ context.Context, owner string) (chrysom.Items, error) {
	if f.err != nil {
		return nil, f.err
	}
	if owner != "" {
		return f.items[owner], nil
	}
	var all chrysom.Items
	for _, items := range f.items {
		all = append(all, items...)
	}

	return all, nil
}

// fakeRegistry is a registry over manifests all owned by "owner-a".
func fakeRegistry(t *testing.T, manifests []schema.Manifest, err error) *ancla.Registry {
	t.Helper()
	return fakeRegistryOwned(t, map[string][]schema.Manifest{"owner-a": manifests}, err)
}

func fakeRegistryOwned(t *testing.T, byOwner map[string][]schema.Manifest, err error) *ancla.Registry {
	t.Helper()
	store := fakeStore{items: map[string]chrysom.Items{}, err: err}
	for owner, manifests := range byOwner {
		for _, m := range manifests {
			item, cerr := schema.SchemaToItemFormat(time.Now, m, schema.OutputLegacy)
			require.NoError(t, cerr)
			store.items[owner] = append(store.items[owner], item)
		}
	}

	return ancla.NewRegistry(store)
}

func v1Manifest(url, secret string, partners ...string) *schema.ManifestV1 {
	var r webhook.RegistrationV1
	r.Config.ReceiverURL = url
	r.Config.Secret = secret
	r.Events = []string{"x"}

	return &schema.ManifestV1{PartnerIDs: partners, Registration: r}
}

func v2Manifest(name, secret string, partners ...string) *schema.ManifestV2 {
	var r webhook.RegistrationV2
	r.CanonicalName = name
	r.Webhooks = []webhook.Webhook{{Secret: secret}}

	return &schema.ManifestV2{PartnerIds: partners, Registration: r}
}

func TestGetAllWebhooksHandler(t *testing.T) {
	manifests := []schema.Manifest{
		v1Manifest("http://a.example/hook", "secret-a", "partner-a"),
		v1Manifest("http://b.example/hook", "secret-b", "partner-b"),
		v1Manifest("http://ab.example/hook", "secret-ab", "partner-a", "partner-b"),
		v1Manifest("http://none.example/hook", "secret-none"),
		v2Manifest("v2-b", "secret-v2", "partner-b"),
	}

	list := func(t *testing.T, h http.Handler, partners []string, hasPartners bool) (int, []map[string]any, string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "/api/v3/hooks", nil)
		if hasPartners {
			req = req.WithContext(anclaauth.SetPartnerIDs(req.Context(), partners))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		var listed []map[string]any
		if rec.Code == http.StatusOK {
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &listed), rec.Body.String())
		}

		return rec.Code, listed, rec.Body.String()
	}

	urlsOf := func(listed []map[string]any) []string {
		urls := make([]string, 0, len(listed))
		for _, l := range listed {
			if name, ok := l["canonical_name"].(string); ok && name != "" {
				urls = append(urls, name)
				continue
			}
			config, _ := l["config"].(map[string]any)
			url, _ := config["url"].(string)
			urls = append(urls, url)
		}

		return urls
	}

	h := newGetAllWebhooksHandler(fakeRegistry(t, manifests, nil), false, false)

	t.Run("a caller sees only its partners' webhooks", func(t *testing.T) {
		code, listed, body := list(t, h, []string{"partner-a"}, true)
		assert.Equal(t, http.StatusOK, code)
		assert.ElementsMatch(t, []string{"http://a.example/hook", "http://ab.example/hook"}, urlsOf(listed))
		assert.NotContains(t, body, "secret-", "secrets are obfuscated")
		assert.Contains(t, body, "<obfuscated>")
	})

	t.Run("a caller for several partners sees all of theirs", func(t *testing.T) {
		code, listed, _ := list(t, h, []string{"partner-a", "partner-b"}, true)
		assert.Equal(t, http.StatusOK, code)
		assert.ElementsMatch(t, []string{"http://a.example/hook", "http://b.example/hook", "http://ab.example/hook", "v2-b"}, urlsOf(listed))
	})

	t.Run("a caller with no partners sees an empty list", func(t *testing.T) {
		code, listed, body := list(t, h, []string{}, true)
		assert.Equal(t, http.StatusOK, code)
		assert.Empty(t, listed)
		assert.JSONEq(t, "[]", body, "an empty list, not null")
	})

	t.Run("no partners in the context is refused", func(t *testing.T) {
		code, _, body := list(t, h, nil, false)
		assert.Equal(t, http.StatusBadRequest, code)
		assert.Contains(t, body, "failed getting partnerIDs")
	})

	t.Run("partner ids disabled lists everything", func(t *testing.T) {
		all := newGetAllWebhooksHandler(fakeRegistry(t, manifests, nil), true, false)
		code, listed, body := list(t, all, nil, false)
		assert.Equal(t, http.StatusOK, code)
		assert.Len(t, listed, len(manifests))
		assert.NotContains(t, body, "secret-")
	})

	t.Run("a store failure is a 500 without the cause", func(t *testing.T) {
		failing := newGetAllWebhooksHandler(fakeRegistry(t, nil, errors.New("argus at http://argus:6600 is down")), false, false)
		code, _, body := list(t, failing, []string{"partner-a"}, true)
		assert.Equal(t, http.StatusInternalServerError, code)
		assert.NotContains(t, body, "argus:6600")
		assert.Contains(t, body, "failed to list webhooks")
	})
}

func TestGetAllWebhooksHandlerFilterByOwner(t *testing.T) {
	reg := fakeRegistryOwned(t, map[string][]schema.Manifest{
		"owner-a": {v1Manifest("http://a.example/hook", "s", "partner-a")},
		"owner-b": {v1Manifest("http://b.example/hook", "s", "partner-a")},
	}, nil)
	h := newGetAllWebhooksHandler(reg, true, true)

	get := func(principal string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/api/v3/hooks", nil)
		if principal != "" {
			req = req.WithContext(anclaauth.SetPrincipal(req.Context(), principal))
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)

		return rec.Code, rec.Body.String()
	}

	t.Run("a caller sees only its own", func(t *testing.T) {
		code, body := get("owner-a")
		assert.Equal(t, http.StatusOK, code)
		assert.Contains(t, body, "a.example")
		assert.NotContains(t, body, "b.example")
	})

	t.Run("no principal is refused, never listing everyone's", func(t *testing.T) {
		code, body := get("")
		assert.Equal(t, http.StatusUnauthorized, code)
		assert.NotContains(t, body, "example/hook")
	})
}

func TestGetAllWebhooksHandlerDurationByVersion(t *testing.T) {
	m := v1Manifest("http://a.example/hook", "secret-a", "partner-a")
	m.Registration.Duration = webhook.CustomDuration(5 * time.Minute)
	h := newGetAllWebhooksHandler(fakeRegistry(t, []schema.Manifest{m, v2Manifest("v2", "s", "partner-a")}, nil), true, false)

	for version, want := range map[string]string{
		"v2": `"duration":300000000000`, // nanoseconds, as before the webhook-schema upgrade
		"v3": `"duration":"5m0s"`,
	} {
		t.Run(version, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/"+version+"/hooks", nil)
			req = mux.SetURLVars(req, map[string]string{"version": version})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Contains(t, rec.Body.String(), want)
			assert.Contains(t, rec.Body.String(), `"canonical_name":"v2"`, "a v2-schema registration is listed as it is")
			assert.Equal(t, 1, strings.Count(rec.Body.String(), `"duration"`), "one duration per v1 registration")
		})
	}
}

func TestSharesPartner(t *testing.T) {
	assert.True(t, sharesPartner([]string{"a", "b"}, []string{"b"}))
	assert.False(t, sharesPartner([]string{"a"}, []string{"b"}))
	assert.False(t, sharesPartner(nil, []string{"b"}))
	assert.False(t, sharesPartner([]string{"a"}, nil))
}
