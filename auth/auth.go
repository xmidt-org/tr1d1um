// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"regexp"

	"github.com/justinas/alice"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/bascule"
	"github.com/xmidt-org/bascule/basculehttp"
	"github.com/xmidt-org/bascule/basculehttp/basculecaps"
	"github.com/xmidt-org/bascule/basculejwt"
	"github.com/xmidt-org/clortho"
	"go.uber.org/zap"
)

const (
	inboundConfigKey = "auth.inbound"
	basicConfigKey   = "auth.inbound.Basic"
	jwtConfigKey     = "auth.inbound.JWT"
	clorthoConfigKey = "auth.inbound.JWT.Clortho"
)

type inboundConfig struct {
	JWT   jwtConfig
	Basic []string
}

type jwtConfig struct {
	Capabilities    []string
	EndpointBuckets []string
}

func NewMiddleware(cfg inboundConfig, v *viper.Viper, kr clortho.KeyRing, l *zap.Logger, counter *prometheus.CounterVec) (alice.Chain, error) {
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
		basicAllowed := make(map[string]string)
		for _, a := range cfg.Basic {
			if len(a) == 0 {
				continue
			}

			decoded, err := base64.StdEncoding.DecodeString(a)
			if err != nil {
				l.Info("failed to decode auth header", zap.Any("authHeader", a), zap.Error(err))
				continue
			}

			i := bytes.IndexByte(decoded, ':')
			if i > 0 {
				basicAllowed[string(decoded[:i])] = string(decoded[i+1:])
			}
		}

		if len(basicAllowed) == 0 {
			return alice.Chain{}, fmt.Errorf("`%s` must contain at least 1 valid basic cred", basicConfigKey)
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithBasic())
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(basicSchemeValidator))
		authorizerOpts = append(authorizerOpts,
			bascule.WithApproverFuncs(basicPasswordValidator(basicAllowed)))
	} else if v.IsSet(jwtConfigKey) {
		kp, err := clortho.NewKeyProvider(clortho.WithRingKey(kr))
		if err != nil {
			return alice.Chain{}, fmt.Errorf("error setting up clortho KeyProvider: %v", err)
		}

		jwtParseOpts = append(jwtParseOpts, jwt.WithKeyProvider(kp))
		jwtp, err := basculejwt.NewTokenParser(jwtParseOpts...)
		if err != nil {
			return alice.Chain{}, fmt.Errorf("error setting up JWT parser: %v", err)
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithScheme(basculehttp.SchemeBearer, jwtp))
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(bearerSchemeValidator))

		approver, err := basculecaps.NewApprover(
			basculecaps.WithCapabilities(cfg.JWT.Capabilities...))
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
