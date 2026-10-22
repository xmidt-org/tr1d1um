// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package compliance

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/stretchr/testify/require"
)

// The capability prefixes tr1d1um.yaml configures.
const (
	apiPrefix   = "webpa-prefix-api:"
	cidrPrefix  = "webpa-prefix-cidr:"
	ratePrefix  = "webpa-prefix-rate:"
	paramPrefix = "webpa-prefix-param:"
)

// The endpoint capabilities, without their prefix.  The \b keeps a
// capability for config from granting configX.
const (
	allCapability       = ".*:all"
	configAllCapability = `device/.*/config\b:all`
	configGetCapability = `device/.*/config\b:get`
	statGetCapability   = `device/.*/stat\b:get`
	hookPostCapability  = `hook\b:post`
	hooksGetCapability  = `hooks\b:get`
)

// The partner every token names unless a test says otherwise.
const defaultPartner = "partner-a"

// The principal every token names unless a test says otherwise.  The
// overrides in tr1d1um.yaml are keyed by other principals.
const defaultPrincipal = "compliance-caller"

// tokenSigner mints the tokens tr1d1um trusts: its public key is in the
// configuration as a fixed clortho key.
type tokenSigner struct {
	key       *rsa.PrivateKey
	keyID     string
	publicPEM string
}

func newTokenSigner() (*tokenSigner, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return nil, err
	}

	return &tokenSigner{
		key:       key,
		keyID:     "compliance",
		publicPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})),
	}, nil
}

// claims describes the token a test wants.
type claims struct {
	// Subject is the principal; defaultPrincipal when empty.
	Subject string

	// Partners are allowedResources.allowedPartners; defaultPartner when nil.
	// noPartners leaves the claim out.
	Partners []string

	// Capabilities are written as given, prefix and all.
	Capabilities []string

	// IssuedAt is the iat claim; now when zero.
	IssuedAt time.Time

	// Expiration is the exp claim; an hour from now when zero.
	Expiration time.Time

	// noIssuedAt and noPartners leave the claim out altogether.
	noIssuedAt bool
	noPartners bool
}

// api prefixes endpoint capabilities.
func api(caps ...string) []string {
	out := make([]string, 0, len(caps))
	for _, c := range caps {
		out = append(out, apiPrefix+c)
	}

	return out
}

// mint signs a token with the given claims and returns it as an
// Authorization header value.
func (s *tokenSigner) mint(t *testing.T, c claims) string {
	t.Helper()

	if c.Subject == "" {
		c.Subject = defaultPrincipal
	}
	if c.Partners == nil {
		c.Partners = []string{defaultPartner}
	}
	now := time.Now()
	if c.IssuedAt.IsZero() {
		c.IssuedAt = now
	}
	if c.Expiration.IsZero() {
		c.Expiration = now.Add(time.Hour)
	}

	b := jwt.NewBuilder().
		Issuer("compliance").
		Subject(c.Subject).
		JwtID(randomHex(8)).
		Expiration(c.Expiration).
		Claim("capabilities", c.Capabilities)
	if !c.noIssuedAt {
		b = b.IssuedAt(c.IssuedAt)
	}
	if !c.noPartners {
		b = b.Claim("allowedResources", map[string]any{"allowedPartners": c.Partners})
	}
	tok, err := b.Build()
	require.NoError(t, err)

	headers := jws.NewHeaders()
	require.NoError(t, headers.Set(jws.KeyIDKey, s.keyID))
	signed, err := jwt.Sign(tok, jwt.WithKey(jwa.RS256(), s.key, jws.WithProtectedHeaders(headers)))
	require.NoError(t, err)

	return "Bearer " + string(signed)
}

// token mints a token with the given endpoint capabilities and the defaults
// for everything else.
func token(t *testing.T, caps ...string) string {
	t.Helper()
	return signer.mint(t, claims{Capabilities: api(caps...)})
}

// allToken mints a token that may call every endpoint.
func allToken(t *testing.T) string {
	t.Helper()
	return token(t, allCapability)
}

// forgedClaims are claims that would grant everything if the signature were
// honored.
func forgedClaims() map[string]any {
	return map[string]any{
		"iss":              "compliance",
		"sub":              defaultPrincipal,
		"iat":              time.Now().Unix(),
		"exp":              time.Now().Add(time.Hour).Unix(),
		"capabilities":     api(allCapability),
		"allowedResources": map[string]any{"allowedPartners": []string{defaultPartner}},
	}
}

func b64(data []byte) string {
	return base64.RawURLEncoding.EncodeToString(data)
}

// forgeRS256Token signs claims with a fresh key no server trusts, naming the
// trusted key's ID so only the signature is wrong.
func forgeRS256Token(t *testing.T, c map[string]any) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	header, err := json.Marshal(map[string]string{"alg": "RS256", "typ": "JWT", "kid": signer.keyID})
	require.NoError(t, err)
	body, err := json.Marshal(c)
	require.NoError(t, err)

	signingInput := b64(header) + "." + b64(body)
	digest := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
	require.NoError(t, err)

	return signingInput + "." + b64(sig)
}

// forgeUnsignedToken builds an alg=none token.
func forgeUnsignedToken(t *testing.T, c map[string]any) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "none", "typ": "JWT"})
	require.NoError(t, err)
	body, err := json.Marshal(c)
	require.NoError(t, err)

	return b64(header) + "." + b64(body) + "."
}

// jwtParts splits a bearer token into its three parts.
func jwtParts(t *testing.T, bearer string) []string {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(bearer, "Bearer "), ".")
	require.Len(t, parts, 3)

	return parts
}
