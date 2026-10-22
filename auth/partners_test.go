// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/bascule"
)

func TestPartnerIDs(t *testing.T) {
	header := http.Header{}
	header.Add(PartnerIDHeader, "partner-a, partner-b")
	header.Add(PartnerIDHeader, "")
	header.Add("X-Xmidt-Partner-Ids", "partner-c")

	t.Run("basic token reads the headers", func(t *testing.T) {
		partners, ok := PartnerIDs(testBasicToken{user: "user"}, header)
		assert.True(t, ok)
		assert.Equal(t, []string{"partner-a", "partner-b", "partner-c"}, partners)
	})

	t.Run("basic token without headers has no partners", func(t *testing.T) {
		partners, ok := PartnerIDs(testBasicToken{user: "user"}, http.Header{})
		assert.True(t, ok)
		assert.Empty(t, partners)
		assert.NotNil(t, partners, "an empty list, so ancla sees a caller with no partners rather than none at all")
	})

	t.Run("nil token reads the headers", func(t *testing.T) {
		partners, ok := PartnerIDs(nil, header)
		assert.True(t, ok)
		assert.Len(t, partners, 3)
	})

	t.Run("jwt reads its claim and ignores the headers", func(t *testing.T) {
		token := testClaimsToken(t, map[string]any{allowedResources: map[string]any{allowedPartners: []string{"claimed"}}})
		partners, ok := PartnerIDs(token, header)
		assert.True(t, ok)
		assert.Equal(t, []string{"claimed"}, partners)
	})

	t.Run("jwt without the claim", func(t *testing.T) {
		_, ok := PartnerIDs(testClaimsToken(t, nil), header)
		assert.False(t, ok)
	})

	t.Run("jwt with an unusable claim", func(t *testing.T) {
		token := testClaimsToken(t, map[string]any{allowedResources: map[string]any{allowedPartners: map[string]any{"not": "a list"}}})
		_, ok := PartnerIDs(token, header)
		assert.False(t, ok)
	})

	t.Run("token without attributes", func(t *testing.T) {
		_, ok := PartnerIDs(&testRateToken{principal: "x"}, header)
		assert.False(t, ok)
	})
}

func TestWithPartnerIDs(t *testing.T) {
	serve := func(token bascule.Token, header http.Header) (principal string, hasPrincipal bool, partners []string, hasPartners bool) {
		h := withPartnerIDs(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
			principal, hasPrincipal = anclaauth.GetPrincipal(r.Context())
			partners, hasPartners = anclaauth.GetPartnerIDs(r.Context())
		}))

		req := httptest.NewRequest(http.MethodGet, testPath, nil)
		req.Header = header
		if token != nil {
			req = req.WithContext(bascule.WithToken(req.Context(), token))
		}
		h.ServeHTTP(httptest.NewRecorder(), req)

		return
	}

	t.Run("jwt", func(t *testing.T) {
		token := testClaimsToken(t, map[string]any{allowedResources: map[string]any{allowedPartners: []string{"claimed"}}})
		principal, hasPrincipal, partners, hasPartners := serve(token, http.Header{PartnerIDHeader: {"spoofed"}})
		require.True(t, hasPrincipal)
		assert.Equal(t, "client", principal)
		require.True(t, hasPartners)
		assert.Equal(t, []string{"claimed"}, partners)
	})

	t.Run("basic", func(t *testing.T) {
		principal, hasPrincipal, partners, hasPartners := serve(testBasicToken{user: "user"}, http.Header{PartnerIDHeader: {"from-header"}})
		require.True(t, hasPrincipal)
		assert.Equal(t, "user", principal)
		require.True(t, hasPartners)
		assert.Equal(t, []string{"from-header"}, partners)
	})

	t.Run("jwt without a claim sets no partners", func(t *testing.T) {
		_, hasPrincipal, _, hasPartners := serve(testClaimsToken(t, nil), http.Header{})
		assert.True(t, hasPrincipal)
		assert.False(t, hasPartners)
	})

	t.Run("no token sets nothing", func(t *testing.T) {
		_, hasPrincipal, _, hasPartners := serve(nil, http.Header{PartnerIDHeader: {"x"}})
		assert.False(t, hasPrincipal)
		assert.False(t, hasPartners)
	})
}
