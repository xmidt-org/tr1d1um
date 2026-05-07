// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rsa"
	"errors"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v4"
	"github.com/justinas/alice"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/clortho"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

var possiblePrefixURLs = []string{
	"/" + apiBase,
	"/" + prevAPIBase,
}

// JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// Config is used to create the clortho Resolver & Refresher for JWT verification keys
	Config clortho.Config

	// Leeway is used to set the amount of time buffer should be given to JWT
	// time values, such as nbf
	// Note: Leeway was removed in Bascule v1.1.1
	// It was unused in Tr1d1um and can be manually configured if needed.
	// Leeway bascule.Leeway

}

// JWTToken implements bascule.Token
type JWTToken struct {
	principal string
}

// Principal returns the subject claim from the JWT
func (jt *JWTToken) Principal() string {
	return jt.principal
}

func provideAuthChain() fx.Option {
	return fx.Options(
		fx.Provide(
			arrange.UnmarshalKey("jwtValidator", JWTValidator{}),
			func(c JWTValidator) clortho.Config {
				return c.Config
			},
			func(config clortho.Config, logger *zap.Logger) (*basculehttp.Middleware, error) {
				return createAuthMiddleware(config, logger)
			},
			fx.Annotated{
				Name: "auth_chain",
				Target: func(middleware *basculehttp.Middleware) alice.Chain {
					return alice.New(middleware.Then)
				},
			},
		),
	)
}

// createAuthMiddleware creates a properly configured Bascule middleware with JWT support
func createAuthMiddleware(config clortho.Config, logger *zap.Logger) (*basculehttp.Middleware, error) {
	// Create Clortho resolver for JWT key
	resolver, err := clortho.NewResolver(
		clortho.WithConfig(config),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWT key resolver: %w", err)
	}

	// Create JWT token parser
	jwtParser := &JWTTokenParser{
		resolver: resolver,
		logger:   logger,
	}

	// Create authorization parser with JWT support
	authParser, err := basculehttp.NewAuthorizationParser(
		basculehttp.WithScheme(basculehttp.SchemeBearer, jwtParser),
		basculehttp.WithBasic(), // Also support basic auth
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create authorization parser: %w", err)
	}

	// Create authenticator with JWT parser
	authenticator, err := basculehttp.NewAuthenticator(
		bascule.WithTokenParsers(authParser),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	// Create middleware with error handling
	return basculehttp.NewMiddleware(
		basculehttp.WithAuthenticator(authenticator),
		basculehttp.WithErrorStatusCoder(
			func(r *http.Request, err error) int {
				if errors.Is(err, bascule.ErrMissingCredentials) {
					return 401
				}
				if errors.Is(err, bascule.ErrBadCredentials) {
					return 401
				}
				if errors.Is(err, bascule.ErrInvalidCredentials) {
					return 400
				}
				return 500
			},
		),
		basculehttp.WithChallenges(
			basculehttp.Challenge{
				Scheme: "Bearer",
				Parameters: func() basculehttp.ChallengeParameters {
					var cp basculehttp.ChallengeParameters
					cp.SetRealm("xmidt")
					return cp
				}(),
			},
		),
	)
}

// JWTTokenParser implements bascule.TokenParser[string] for JWT tokens
type JWTTokenParser struct {
	resolver clortho.Resolver
	logger   *zap.Logger
}

// Parse parses and validates a JWT token string
func (jtp *JWTTokenParser) Parse(ctx context.Context, raw string) (bascule.Token, error) {
	if raw == "" {
		return nil, bascule.ErrMissingCredentials
	}

	// Parse the JWT token without verification first to get the key ID
	token, err := jwt.ParseWithClaims(raw, jwt.MapClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate the signing method
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		// Get the key ID from the token header
		keyID, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing or invalid key ID in JWT header")
		}

		// Resolve the public key using Clortho
		clorthoKey, err := jtp.resolver.Resolve(ctx, keyID)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve JWT signing key: %w", err)
		}

		// Extract the actual crypto key from the Clortho key
		// For RSA keys, we need to get the underlying public key
		var publicKey interface{}
		switch k := clorthoKey.(type) {
		case interface{ PublicKey() *rsa.PublicKey }:
			publicKey = k.PublicKey()
		case interface{ Key() interface{} }:
			publicKey = k.Key()
		default:
			return nil, fmt.Errorf("unsupported key type: %T", clorthoKey)
		}

		// Ensure we have an RSA public key
		rsaKey, ok := publicKey.(*rsa.PublicKey)
		if !ok {
			return nil, fmt.Errorf("expected RSA public key, got %T", publicKey)
		}

		return rsaKey, nil
	})

	if err != nil {
		jtp.logger.Error("JWT parsing failed", zap.Error(err))
		return nil, bascule.ErrInvalidCredentials
	}

	if !token.Valid {
		return nil, bascule.ErrBadCredentials
	}

	// Extract claims
	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return nil, bascule.ErrInvalidCredentials
	}

	// Extract principal (subject)
	principal, _ := claims["sub"].(string)
	if principal == "" {
		// Fallback to other possible principal fields
		if user, ok := claims["user"].(string); ok {
			principal = user
		} else if username, ok := claims["username"].(string); ok {
			principal = username
		} else {
			principal = "unknown"
		}
	}

	jtp.logger.Debug("JWT token validated",
		zap.String("principal", principal))

	return &JWTToken{
		principal: principal,
	}, nil
}
