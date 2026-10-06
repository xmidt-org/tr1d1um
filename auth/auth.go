// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/justinas/alice"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculehttp/basculecaps"
	"github.com/xmidt-org/bascule/basculejwt"
	"go.uber.org/zap"
)

const (
	inboundConfigKey = "auth.inbound"
	basicConfigKey   = "auth.inbound.Basic"
	jwtConfigKey     = "auth.inbound.JWT"
	clorthoConfigKey = "auth.inbound.JWT.Clortho"
)

var stripAPIVersionRegex = regexp.MustCompile(`^/api/v[0-9]+`)

type inboundConfig struct {
	JWT   jwtConfig
	Basic []string
}

type jwtConfig struct {
	AcceptAllMethod string
	CacheSize       int
	Prefixes        []string
	EndpointBuckets []string
}

// NewMiddleware builds the inbound authentication and authorization chain.
//
// Exactly one of basic or JWT authentication must be configured.  Basic
// credentials are checked against the configured list.  A JWT must verify
// against a key the provider supplies, carry a capability granting the
// request, and state the partner IDs its bearer may act for.
func NewMiddleware(cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider, l *zap.Logger, counter *prometheus.CounterVec) (alice.Chain, error) {
	var (
		authorizationParserOpts []basculehttp.AuthorizationParserOption
		authorizerOpts          []bascule.AuthorizerOption[*http.Request]
		validatorOpts           []bascule.Validator[*http.Request]
		jwtParseOpts            []jwt.ParseOption
		endpointBuckets         []*regexp.Regexp
	)

	if v.IsSet(basicConfigKey) && v.IsSet(jwtConfigKey) {
		return alice.Chain{}, fmt.Errorf("`%s` and `%s` can't both be set", basicConfigKey, jwtConfigKey)
	} else if v.IsSet(basicConfigKey) {
		basicAllowed, err := decodeBasicCredentials(cfg.Basic)
		if err != nil {
			return alice.Chain{}, err
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithBasic())
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(basicSchemeValidator))
		authorizerOpts = append(authorizerOpts,
			bascule.WithApproverFuncs(basicPasswordValidator(basicAllowed)))

		l.Info("inbound basic auth enabled", zap.Int("credentials", len(basicAllowed)))
	} else if v.IsSet(jwtConfigKey) {
		if kp == nil {
			return alice.Chain{}, fmt.Errorf("`%s` must configure at least one key provider", clorthoConfigKey)
		}

		jwtParseOpts = append(jwtParseOpts, jwt.WithKeyProvider(kp))
		jwtp, err := basculejwt.NewTokenParser(jwtParseOpts...)
		if err != nil {
			return alice.Chain{}, fmt.Errorf("error setting up JWT parser: %v", err)
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithScheme(basculehttp.SchemeBearer, jwtp))
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(bearerSchemeValidator))

		approver, err := basculecaps.NewApprover(capabilityOptions(cfg.JWT)...)
		if err != nil {
			return alice.Chain{}, fmt.Errorf("error setting up JWT capability checks: %v", err)
		}

		authorizerOpts = append(authorizerOpts,
			bascule.WithApprovers(approver),
			bascule.WithApproverFuncs(jwtClaimPartnerIDsValidator))
		for _, pattern := range cfg.JWT.EndpointBuckets {
			re, err := regexp.Compile(pattern)
			if err != nil {
				l.Error("failed to compile endpoint bucket regex", zap.String("regex", pattern), zap.Error(err))
				continue
			}

			endpointBuckets = append(endpointBuckets, re)
		}
	} else {
		return alice.Chain{}, fmt.Errorf("either `%s` or `%s` must be set, but not both", basicConfigKey, jwtConfigKey)
	}

	tp, err := basculehttp.NewAuthorizationParser(authorizationParserOpts...)
	if err != nil {
		return alice.Chain{}, fmt.Errorf("error setting up authorization parser: %v", err)
	}

	validatorOpts = append(validatorOpts, basculehttp.AsValidator(authPrincipalValidator))
	authorizerOpts = append(authorizerOpts,
		bascule.WithAuthorizeListeners(
			authorizerEvent{
				l:         l,
				counter:   counter,
				endpoints: endpointBuckets}))
	auth, err := basculehttp.NewMiddleware(
		basculehttp.UseAuthenticator(basculehttp.NewAuthenticator(
			bascule.WithTokenParsers(tp),
			bascule.WithValidators(validatorOpts...),
			bascule.WithAuthenticateListeners(
				authenticatorEvent{
					l:          l,
					counter:    counter,
					parserOpts: jwtParseOpts,
					endpoints:  endpointBuckets}),
		)),
		basculehttp.UseAuthorizer(basculehttp.NewAuthorizer(authorizerOpts...)),
	)
	if err != nil {
		return alice.Chain{}, fmt.Errorf("failed to create auth middleware: %v", err)
	}

	return alice.New(setLogger(l), auth.Then), nil
}

// decodeBasicCredentials turns the configured list of base64 "user:password"
// strings into a map of user name to password.  A malformed entry is an error
// so that a bad credential list is caught at startup rather than silently
// refusing logins later.  The entries are credentials, so an error names the
// position of the bad one and never its value.
func decodeBasicCredentials(encoded []string) (map[string]string, error) {
	allowed := make(map[string]string, len(encoded))

	for i, e := range encoded {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}

		decoded, err := base64.StdEncoding.DecodeString(e)
		if err != nil {
			return nil, fmt.Errorf("`%s[%d]`: not valid base64: %w", basicConfigKey, i, err)
		}

		user, password, found := strings.Cut(string(decoded), ":")
		if !found || user == "" {
			return nil, fmt.Errorf("`%s[%d]`: expected \"user:password\"", basicConfigKey, i)
		}

		allowed[user] = password
	}

	if len(allowed) == 0 {
		return nil, fmt.Errorf("`%s` must contain at least 1 valid basic cred", basicConfigKey)
	}

	return allowed, nil
}

// capabilityOptions builds the capability approver's options.  The approver
// rejects a blank all-method value and a non-positive cache size, so each is
// passed only when configured and the approver's default applies otherwise.
func capabilityOptions(cfg jwtConfig) []basculecaps.ApproverOption {
	opts := []basculecaps.ApproverOption{
		basculecaps.WithURLNormalizeFunc(stripAPIVersion),
		basculecaps.WithPrefixes(cfg.Prefixes...),
	}

	if cfg.AcceptAllMethod != "" {
		opts = append(opts, basculecaps.WithAllMethod(cfg.AcceptAllMethod))
	}

	if cfg.CacheSize > 0 {
		opts = append(opts, basculecaps.WithCacheSize(cfg.CacheSize))
	}

	return opts
}

// stripAPIVersion removes a leading /api/vN from a request's path, so that
// capabilities may be written without it.
func stripAPIVersion(u url.URL) url.URL {
	u.Path = stripAPIVersionRegex.ReplaceAllString(u.Path, "")
	u.RawPath = ""

	return u
}
