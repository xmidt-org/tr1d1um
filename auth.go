// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"crypto/rsa"
	"fmt"
	"net/http"

	"github.com/golang-jwt/jwt/v4"
	"github.com/justinas/alice"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/clortho"
	"go.uber.org/fx"
	"go.uber.org/zap"
)

// JWTValidator provides a convenient way to define jwt validator through config files
type JWTValidator struct {
	// Config is used to create the clortho Resolver & Refresher for JWT verification keys
	Config clortho.Config

	// Leeway (jwtValidator.leeway) allowed a clock-skew tolerance on the exp,
	// nbf and iat claims.  bascule v0.11 read it here and applied it to every
	// bearer token; bascule v1.1.1 dropped the type, and it is not restored
	// because no deployment configures it -- every value was zero, so JWT time
	// validation behaves the same without it.
	//
	// If a deployment ever needs skew tolerance, reinstate it as a jwt.Claims
	// implementation whose Valid() offsets time.Now() before calling
	// VerifyExpiresAt/VerifyIssuedAt/VerifyNotBefore, which golang-jwt/v4 still
	// provides with the same signatures bascule used.

}

// JWT claims read from a token.
const (
	// capabilitiesClaim holds the token's capabilities.  It matches
	// basculejwt.CapabilitiesKey.
	capabilitiesClaim = "capabilities"
)

// JWTToken implements bascule.Token, bascule.CapabilitiesAccessor and
// bascule.AttributesAccessor
type JWTToken struct {
	principal    string
	capabilities []string
	claims       map[string]any
}

// Principal returns the subject claim from the JWT
func (jt *JWTToken) Principal() string {
	return jt.principal
}

// Capabilities returns the capabilities claim, which the authorizer uses to
// decide what this token is allowed to do.  Returns nil when the token carries
// none.
func (jt *JWTToken) Capabilities() []string {
	return jt.capabilities
}

// Get returns a claim by name, satisfying bascule.AttributesAccessor.  Callers
// that need a nested claim should use bascule.GetAttribute, which walks the
// path for them.
func (jt *JWTToken) Get(key string) (any, bool) {
	v, ok := jt.claims[key]
	return v, ok
}

func provideAuthChain() fx.Option {
	return fx.Options(
		fx.Provide(
			arrange.UnmarshalKey("jwtValidator", JWTValidator{}),
			arrange.UnmarshalKey(capabilityCheckKey, CapabilityConfig{}),
			arrange.UnmarshalKey(authxInboundKey, InboundAuthConfig{}),
			func(c JWTValidator) clortho.Config {
				return c.Config
			},
			func(in authMiddlewareIn) (*basculehttp.Middleware, error) {
				metric, err := newCapabilityMetric(in.CapabilityChecks, in.Capabilities.EndpointBuckets)
				if err != nil {
					return nil, fmt.Errorf("capabilityCheck: endpointBuckets: %w", err)
				}
				return createAuthMiddleware(in.Config, in.Capabilities, in.Inbound, in.Logger, metric, in.AuthOutcomes)
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
// authMiddlewareIn collects everything the auth middleware needs from fx.
type authMiddlewareIn struct {
	fx.In
	Config           clortho.Config
	Capabilities     CapabilityConfig
	Inbound          InboundAuthConfig
	Logger           *zap.Logger
	CapabilityChecks *prometheus.CounterVec `name:"capability_check"`
	AuthOutcomes     *prometheus.CounterVec `name:"auth_outcome"`
}

func createAuthMiddleware(config clortho.Config, capabilities CapabilityConfig, inbound InboundAuthConfig, logger *zap.Logger, metric capabilityMetric, authOutcomes *prometheus.CounterVec) (*basculehttp.Middleware, error) {
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

	// Assemble the accepted schemes.  Bearer is always available.
	//
	// Basic is offered only when credentials are configured, and it is
	// registered together with the validator that checks them.  These must
	// stay coupled: basculehttp's Basic parser only base64-decodes a
	// credential and never checks a password, so offering the scheme without
	// a validator accepts any "Authorization: Basic <anything>".
	parserOptions := []basculehttp.AuthorizationParserOption{
		basculehttp.WithScheme(basculehttp.SchemeBearer, jwtParser),
	}
	var validators []bascule.Validator[*http.Request]

	if len(inbound.Basic) > 0 {
		basicValidator, err := newBasicAuthValidator(inbound.Basic)
		if err != nil {
			return nil, fmt.Errorf("failed to load inbound basic credentials: %w", err)
		}

		parserOptions = append(parserOptions, basculehttp.WithBasic())
		validators = append(validators, basicValidator)

		logger.Info("inbound basic auth enabled",
			zap.Int("credentials", len(inbound.Basic)))
	}

	authParser, err := basculehttp.NewAuthorizationParser(parserOptions...)
	if err != nil {
		return nil, fmt.Errorf("failed to create authorization parser: %w", err)
	}

	authenticator, err := basculehttp.NewAuthenticator(
		bascule.WithTokenParsers(authParser),
		bascule.WithValidators(validators...),
		bascule.WithAuthenticateListenerFuncs(newAuthOutcomeListener(authOutcomes)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create authenticator: %w", err)
	}

	// Authorization: every configured capability label must grant the request.
	approver, err := NewCapabilityApprover(capabilities, logger, metric)
	if err != nil {
		return nil, fmt.Errorf("failed to create capability approver: %w", err)
	}

	authorizer, err := basculehttp.NewAuthorizer(
		bascule.WithApprovers[*http.Request](approver),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create authorizer: %w", err)
	}

	// Create middleware with error handling
	return basculehttp.NewMiddleware(
		basculehttp.WithAuthenticator(authenticator),
		basculehttp.WithAuthorizer(authorizer),
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

		// clortho.Key exposes the underlying key through Public().  The
		// concrete type implements neither PublicKey() *rsa.PublicKey nor
		// Key() interface{}, so type-switching on those rejected every key the
		// resolver returned and no JWT could ever be verified.
		publicKey := clorthoKey.Public()
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

	var capabilities []string
	if v, ok := claims[capabilitiesClaim]; ok {
		capabilities, _ = bascule.GetCapabilities(v)
	}

	return &JWTToken{
		principal:    principal,
		capabilities: capabilities,
		claims:       claims,
	}, nil
}
