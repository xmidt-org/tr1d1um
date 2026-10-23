// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"strings"

	"github.com/spf13/cast"

	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
)

// PartnerIDHeader is the header a caller with a basic credential names its
// partners in, since a basic credential carries none.  A JWT's partners come
// from its claims and the header is ignored.  Several values, or one value
// with commas, are all partners.
const PartnerIDHeader = "X-Xmidt-Partner-Id"

// partnerIDHeaders are the header names read for a basic caller's partners:
// PartnerIDHeader, and the plural ancla's handlers were documented with.
var partnerIDHeaders = []string{PartnerIDHeader, "X-Xmidt-Partner-Ids"}

// PartnerIDs returns the partners the request acts for: a JWT's
// allowedResources.allowedPartners claim, or the PartnerIDHeader values of a
// basic caller.  The second result is false when the token is a JWT without
// a usable claim; the partner ID validator refuses such a token before any
// handler runs, so this only happens outside the chain.
func PartnerIDs(token bascule.Token, header http.Header) ([]string, bool) {
	if _, ok := token.(basculehttp.BasicToken); ok || token == nil {
		return headerPartnerIDs(header), true
	}

	accessor, ok := token.(bascule.AttributesAccessor)
	if !ok {
		return nil, false
	}

	value, ok := bascule.GetAttribute[any](accessor, PartnerKeys...)
	if !ok {
		return nil, false
	}

	partners, err := cast.ToStringSliceE(value)
	if err != nil {
		return nil, false
	}

	return partners, true
}

// headerPartnerIDs reads the partner headers, splitting each value on
// commas and dropping blanks.
func headerPartnerIDs(header http.Header) []string {
	partners := []string{}
	for _, name := range partnerIDHeaders {
		for _, value := range header.Values(name) {
			for partner := range strings.SplitSeq(value, ",") {
				if partner = strings.TrimSpace(partner); partner != "" {
					partners = append(partners, partner)
				}
			}
		}
	}

	return partners
}

// withPartnerIDs puts the caller's principal and partners where the
// webhook handlers read them (ancla's auth package), once bascule has
// verified the token.  Without this a webhook registration has no owner
// and no partners, and ancla refuses it.
func withPartnerIDs(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := request.Context()
		if token, ok := bascule.Get(ctx); ok {
			ctx = anclaauth.SetPrincipal(ctx, token.Principal())
			if partners, ok := PartnerIDs(token, request.Header); ok {
				ctx = anclaauth.SetPartnerIDs(ctx, partners)
			}
		}

		next.ServeHTTP(response, request.WithContext(ctx))
	})
}
