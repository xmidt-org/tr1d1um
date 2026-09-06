// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"

	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
)

// InboundAuthConfig is the authx.inbound configuration.
//
//	authx:
//	  inbound:
//	    basic: ["dXNlcjpwYXNz"]
type InboundAuthConfig struct {
	// Basic is a list of base64-encoded "user:password" credentials that are
	// accepted on inbound requests.  When empty, the Basic scheme is not
	// offered at all.
	Basic []string `mapstructure:"basic"`
}

// basicAuthValidator checks a parsed Basic credential against the configured
// list.  basculehttp's parser only base64-decodes the credential, so without a
// validator every Basic credential authenticates.
type basicAuthValidator struct {
	// credentials maps user name to password.  Passwords are compared in
	// constant time so a wrong password cannot be recovered by timing.
	credentials map[string]string
}

// newBasicAuthValidator decodes the configured credentials.  It fails on a
// malformed entry so a bad credential list is caught at startup rather than
// silently refusing logins later.
func newBasicAuthValidator(encoded []string) (*basicAuthValidator, error) {
	credentials := make(map[string]string, len(encoded))

	for i, e := range encoded {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(e))
		if err != nil {
			return nil, fmt.Errorf("%s.basic[%d]: not valid base64: %w", authxInboundKey, i, err)
		}

		user, password, found := strings.Cut(string(decoded), ":")
		if !found {
			return nil, fmt.Errorf("%s.basic[%d]: expected \"user:password\"", authxInboundKey, i)
		}
		if user == "" {
			return nil, fmt.Errorf("%s.basic[%d]: empty user name", authxInboundKey, i)
		}

		credentials[user] = password
	}

	return &basicAuthValidator{credentials: credentials}, nil
}

// Validate implements bascule.Validator[*http.Request].
//
// Tokens from other schemes are not this validator's concern and are passed
// through untouched, as the bascule.Validator contract describes.
func (v *basicAuthValidator) Validate(_ context.Context, _ *http.Request, token bascule.Token) (bascule.Token, error) {
	basic, ok := token.(basculehttp.BasicToken)
	if !ok {
		return nil, nil
	}

	expected, known := v.credentials[basic.UserName()]

	// Compare even when the user is unknown, against an empty password, so
	// that a valid and an invalid user name take the same time.
	supplied := basic.Password()
	match := subtle.ConstantTimeCompare([]byte(supplied), []byte(expected)) == 1

	if !known || !match {
		return nil, bascule.ErrBadCredentials
	}

	return token, nil
}
