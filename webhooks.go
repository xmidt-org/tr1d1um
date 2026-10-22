// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"slices"

	"go.uber.org/zap"

	"github.com/xmidt-org/ancla"
	anclaauth "github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/sallust"
	webhook "github.com/xmidt-org/webhook-schema"
)

// Errors a webhook listing is refused with.
var (
	errNoPartnerIDs      = errors.New("failed getting partnerIDs")
	errWebhookListFailed = errors.New("failed to list webhooks")
)

// obfuscatedSecretValue stands in for every secret in a listing.
const obfuscatedSecretValue = "<obfuscated>" //nolint:gosec // not a credential, its replacement

// newGetAllWebhooksHandler lists the webhooks the caller may see: those
// registered under a partner the caller acts for, as the token's claim or,
// for a basic credential, the partner header names.  A caller sees nothing
// of another partner's, whatever headers it sends.  With partner IDs
// disabled in the webhook configuration every webhook is listed.  Secrets
// are obfuscated.
func newGetAllWebhooksHandler(s ancla.Service, disablePartnerIDs bool) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		ctx := request.Context()
		response.Header().Set("Content-Type", "application/json")

		partners, ok := anclaauth.GetPartnerIDs(ctx)
		if !ok && !disablePartnerIDs {
			writeWebhookError(response, http.StatusBadRequest, errNoPartnerIDs)
			return
		}

		manifests, err := s.GetAll(ctx)
		if err != nil {
			sallust.Get(ctx).Error("failed to list webhooks", zap.Error(err))
			writeWebhookError(response, http.StatusInternalServerError, errWebhookListFailed)
			return
		}

		visible := make([]schema.Manifest, 0, len(manifests))
		for _, m := range manifests {
			if disablePartnerIDs || sharesPartner(manifestPartners(m), partners) {
				visible = append(visible, m)
			}
		}

		streams := schema.SchemasToWRPEventStreams(visible)
		obfuscateSecrets(streams)
		encoder := json.NewEncoder(response)
		encoder.SetEscapeHTML(false) // so <obfuscated> reads as written
		if err := encoder.Encode(streams); err != nil {
			sallust.Get(ctx).Error("failed to encode the webhook list", zap.Error(err))
		}
	})
}

// manifestPartners returns the partners a webhook was registered under.
func manifestPartners(m schema.Manifest) []string {
	switch r := m.(type) {
	case *schema.ManifestV1:
		return r.PartnerIDs
	case *schema.ManifestV2:
		return r.PartnerIds
	default:
		return nil
	}
}

// sharesPartner reports whether the two lists have a partner in common.
func sharesPartner(registered, caller []string) bool {
	return slices.ContainsFunc(registered, func(p string) bool {
		return slices.Contains(caller, p)
	})
}

// obfuscateSecrets replaces every secret in the listed registrations, which
// are values, so each is replaced in the slice.
func obfuscateSecrets(streams []any) {
	for i, v := range streams {
		switch r := v.(type) {
		case webhook.RegistrationV1:
			r.Config.Secret = obfuscatedSecretValue
			streams[i] = r
		case webhook.RegistrationV2:
			for j := range r.Webhooks {
				r.Webhooks[j].Secret = obfuscatedSecretValue
			}
			streams[i] = r
		}
	}
}

func writeWebhookError(response http.ResponseWriter, code int, err error) {
	response.WriteHeader(code)
	_ = json.NewEncoder(response).Encode(map[string]any{"message": err.Error()})
}
