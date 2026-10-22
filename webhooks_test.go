// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/ancla/schema"
	webhook "github.com/xmidt-org/webhook-schema"
)

// fakeWebhookService is an ancla.Service with a fixed list.
type fakeWebhookService struct {
	manifests []schema.Manifest
	err       error
}

func (f fakeWebhookService) Add(context.Context, string, schema.Manifest) error { return nil }

func (f fakeWebhookService) GetAll(context.Context) ([]schema.Manifest, error) {
	return f.manifests, f.err
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

	h := newGetAllWebhooksHandler(fakeWebhookService{manifests: manifests}, false)

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
		all := newGetAllWebhooksHandler(fakeWebhookService{manifests: manifests}, true)
		code, listed, body := list(t, all, nil, false)
		assert.Equal(t, http.StatusOK, code)
		assert.Len(t, listed, len(manifests))
		assert.NotContains(t, body, "secret-")
	})

	t.Run("a store failure is a 500 without the cause", func(t *testing.T) {
		failing := newGetAllWebhooksHandler(fakeWebhookService{err: errors.New("argus at http://argus:6600 is down")}, false)
		code, _, body := list(t, failing, []string{"partner-a"}, true)
		assert.Equal(t, http.StatusInternalServerError, code)
		assert.NotContains(t, body, "argus:6600")
		assert.Contains(t, body, "failed to list webhooks")
	})
}

func TestSharesPartner(t *testing.T) {
	assert.True(t, sharesPartner([]string{"a", "b"}, []string{"b"}))
	assert.False(t, sharesPartner([]string{"a"}, []string{"b"}))
	assert.False(t, sharesPartner(nil, []string{"b"}))
	assert.False(t, sharesPartner([]string{"a"}, nil))
}
