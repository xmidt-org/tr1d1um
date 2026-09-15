// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/spf13/cast"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculejwt"
	"go.uber.org/multierr"
)

const (
	allowedPartners  = "allowedPartners"
	allowedResources = "allowedResources"
)

var (
	errAuthMissingClaims  = fmt.Errorf("%w: missing partner related keys", bascule.ErrBadCredentials)
	errAuthInvalidClaims  = fmt.Errorf("%w: invalid `%s` claim(s)", bascule.ErrBadCredentials, PartnerKeys)
	errAuthUnknownScheme  = errors.New("unknown auth scheme")
	errAuthEmptyPrincipal = errors.New("empty principal")
)

var PartnerKeys = []string{allowedResources, allowedPartners}

func basicSchemeValidator(ctx context.Context, req *http.Request, token bascule.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch t := token.(type) {
	case basculehttp.BasicToken:
	default:
		return fmt.Errorf("%w: %v: `%T`", bascule.ErrBadCredentials, errAuthUnknownScheme, t)
	}

	return nil
}

func bearerSchemeValidator(ctx context.Context, req *http.Request, token bascule.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	switch t := token.(type) {
	case basculejwt.Claims:
	default:
		return fmt.Errorf("%w: %v: `%T`", bascule.ErrBadCredentials, errAuthUnknownScheme, t)
	}

	return nil
}

func basicPasswordValidator(allowed map[string]string) func(context.Context, *http.Request, bascule.Token) error {
	return func(ctx context.Context, req *http.Request, token bascule.Token) error {
		if err := ctx.Err(); err != nil {
			return err
		}

		basic, ok := token.(basculehttp.BasicToken)
		if !ok {
			return nil
		}

		password, ok := allowed[basic.UserName()]
		// User not found.
		if !ok {
			return bascule.ErrBadCredentials

		}

		if basic.Password() != password {
			return bascule.ErrBadCredentials
		}

		return nil
	}
}

func jwtClaimPartnerIDsValidator(ctx context.Context, req *http.Request, token bascule.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	accessor, ok := token.(bascule.AttributesAccessor)
	if !ok {
		return errAuthMissingClaims
	}

	partnerVal, ok := bascule.GetAttribute[any](accessor, PartnerKeys...)
	if !ok {
		return errAuthMissingClaims
	}

	allowedPartners, err := cast.ToStringSliceE(partnerVal)
	if err != nil {
		return multierr.Append(errAuthInvalidClaims, err)
	}

	if len(allowedPartners) == 0 {
		return fmt.Errorf("%w: should not be empty", errAuthInvalidClaims)
	}

	return nil
}

func authPrincipalValidator(ctx context.Context, token bascule.Token) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if token.Principal() == "" {
		return fmt.Errorf("%w: %v", bascule.ErrBadCredentials, errAuthEmptyPrincipal)
	}

	return nil
}
