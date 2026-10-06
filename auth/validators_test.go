// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculejwt"
)

type testJWT struct {
	jwt.Token
}

func (t testJWT) Principal() string {
	p, _ := t.Token.Subject()
	return p
}

func (t testJWT) Get(k string) (any, bool) {
	return t.Field(k)
}

func (t testJWT) Audience() []string {
	v, _ := t.Token.Audience()

	return v
}

func (t testJWT) Expiration() time.Time {
	v, _ := t.Token.Expiration()

	return v
}

func (t testJWT) IssuedAt() time.Time {
	v, _ := t.Token.IssuedAt()

	return v
}

func (t testJWT) Issuer() string {
	v, _ := t.Token.Issuer()

	return v
}

func (t testJWT) JwtID() string {
	v, _ := t.Token.JwtID()

	return v
}

func (t testJWT) NotBefore() time.Time {
	v, _ := t.Token.NotBefore()

	return v
}

func (t testJWT) Subject() string {
	v, _ := t.Token.Subject()

	return v
}

func (t testJWT) Capabilities() (caps []string) {
	if v, ok := t.Field(basculejwt.CapabilitiesKey); ok {
		caps, _ = bascule.GetCapabilities(v)
	}

	return
}

func TestRequirePartnerIDs(t *testing.T) {
	var tests = []struct {
		name       string
		attrMap    map[string]any
		shouldPass bool
	}{
		{
			name: "partnerIDs",
			attrMap: map[string]any{
				// nolint: goconst
				allowedPartners: []string{"partner0", "partner1"},
			},
			shouldPass: true,
		},

		{
			name: "missing partnerIDs key",
			attrMap: map[string]any{
				allowedResources: map[string]any{},
			},
		},
		{
			name: "no partnerIDs",
			attrMap: map[string]any{
				allowedPartners: []string{},
			},
		},
		{
			name: "malformed partnerIDs field",
			attrMap: map[string]any{
				allowedPartners: map[string]any{"partner0": true},
			},
		},
	}

	ctx := context.Background()

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert := assert.New(t)
			require := require.New(t)
			token, err := jwt.NewBuilder().
				Claim(allowedResources, test.attrMap).
				Subject("client0").
				Issuer("https://example.com").
				Build()
			require.NoError(err, "failed to build test JWT")
			err = jwtClaimPartnerIDsValidator(ctx, nil, testJWT{token})
			if test.shouldPass {
				assert.NoError(err)
			} else {
				assert.Error(err)
			}
		})
	}
}
