// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"net/netip"
	"net/url"
	"regexp"
	"strconv"
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
	inboundConfigKey        = "auth.inbound"
	basicConfigKey          = "auth.inbound.Basic"
	jwtConfigKey            = "auth.inbound.JWT"
	cidrConfigKey           = "auth.inbound.JWT.CIDR"
	trustedProxiesConfigKey = "auth.inbound.JWT.CIDR.TrustedProxies"
	clorthoConfigKey        = "auth.inbound.JWT.Clortho"
)

// WarningHeader is the response header that tells the caller about a problem
// with their token or request, one header per warning, on every response
// whether or not the request was blocked.
const WarningHeader = "X-Webpa-Capability-Warning"

// The modes of a capability kind's check.  Three things make up a mode:
// whether a failure rejects the request or only records that it would have,
// whether the caller is told about problems in a warning header, and whether
// a token with no capability of the kind is unrestricted or rejected.
//
// Every mode logs and counts what it finds; the modes differ in what the
// caller sees.
const (
	// ModePermissive never rejects, and tells the caller nothing.  Every
	// request ModeEnforce would have rejected is logged and counted.  The
	// default, so that a newly configured check is a trial run.
	ModePermissive = "permissive"

	// ModePermissiveWithWarnings is ModePermissive, and also tells the
	// caller, in a warning header, what would have been rejected and which
	// of their capabilities are malformed.
	ModePermissiveWithWarnings = "permissiveWithWarnings"

	// ModeEnforce rejects a request that fails the check, and tells the
	// caller which of their capabilities are malformed.  A token with no
	// capability of the kind is unrestricted.
	ModeEnforce = "enforce"

	// ModeRequired is ModeEnforce, and also rejects a token with no
	// capability of the kind.
	ModeRequired = "required"

	// ModePermissiveRequired is ModePermissive for ModeRequired: nothing is
	// rejected, and every request ModeRequired would have rejected is
	// logged and counted.
	ModePermissiveRequired = "permissiveRequired"

	// ModePermissiveRequiredWithWarnings is ModePermissiveRequired, and
	// also tells the caller.
	ModePermissiveRequiredWithWarnings = "permissiveRequiredWithWarnings"
)

// checkMode is a parsed mode.
type checkMode struct {
	// permissive records a failure instead of rejecting the request.
	permissive bool

	// warnings tells the caller about problems in a warning header.
	warnings bool

	// required rejects a token with no capability of the kind.
	required bool
}

// checkModes maps each mode's configuration value to its meaning.
var checkModes = map[string]checkMode{
	ModePermissive:                     {permissive: true},
	ModePermissiveWithWarnings:         {permissive: true, warnings: true},
	ModeEnforce:                        {warnings: true},
	ModeRequired:                       {warnings: true, required: true},
	ModePermissiveRequired:             {permissive: true, required: true},
	ModePermissiveRequiredWithWarnings: {permissive: true, warnings: true, required: true},
}

// parseMode parses a configured mode, which must be one of accepted.  An
// unset mode is ModePermissive.  The comparison ignores case; key names the
// setting in an error.
func parseMode(mode, key string, accepted ...string) (checkMode, error) {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	if normalized == "" {
		normalized = strings.ToLower(ModePermissive)
	}

	for _, a := range accepted {
		if normalized == strings.ToLower(a) {
			return checkModes[a], nil
		}
	}

	quoted := make([]string, len(accepted))
	for i, a := range accepted {
		quoted[i] = strconv.Quote(a)
	}

	return checkMode{}, fmt.Errorf("`%s.Mode`: expected one of %s, got %q", key, strings.Join(quoted, ", "), mode)
}

// String returns the mode's configuration value.
func (m checkMode) String() string {
	for name, mode := range checkModes {
		if mode == m {
			return name
		}
	}

	return ""
}

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
	CIDR            cidrConfig
	Rate            rateConfig
	Param           paramConfig
}

// cidrConfig is the auth.inbound.JWT.CIDR section: how a token's CIDR
// capabilities limit where its bearer may call from.  The check runs only
// when the section is present.
type cidrConfig struct {
	// Prefixes select a token's CIDR capabilities, e.g. x1:webpa:cidr:.
	// At least one is required.
	Prefixes []string

	// Mode is one of ModePermissive (the default), ModePermissiveWithWarnings,
	// ModeEnforce, ModeRequired, ModePermissiveRequired or
	// ModePermissiveRequiredWithWarnings.  Outside the required modes, a
	// token with no CIDR capability is unrestricted: no network limit
	// applies to it.
	Mode string

	// CacheSize bounds how many parsed capabilities are retained.
	CacheSize int

	// TrustedProxies are the networks of the hops this deployment operates,
	// such as load balancers, and trusts to report the address they received
	// a request from.  The caller origin of a request is the nearest hop that
	// is not one of them.  This is deployment configuration; it is never
	// read from a token.
	TrustedProxies []string
}

// NewMiddleware builds the inbound authentication and authorization chain.
//
// Exactly one of basic or JWT authentication must be configured.  Basic
// credentials are checked against the configured list.  A JWT must verify
// against a key the provider supplies, carry a capability granting the
// request, state the partner IDs its bearer may act for, and, when the CIDR
// check is configured, be used from a network its CIDR capabilities name.
// When the rate check is configured, its bearer is also held to the rates
// its rate capabilities state.
//
// The parameter check is returned separately, because only the device
// routes know which parameters a request names; they apply it with
// ParamCheck.Middleware.  It is nil when not configured.
//
// Warnings raised while authorizing a request are returned to the caller in
// WarningHeader and counted in warnings.
func NewMiddleware(cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider, l *zap.Logger, counter, warnings *prometheus.CounterVec) (alice.Chain, *ParamCheck, error) {
	var (
		authorizationParserOpts []basculehttp.AuthorizationParserOption
		authorizerOpts          []bascule.AuthorizerOption[*http.Request]
		validatorOpts           []bascule.Validator[*http.Request]
		jwtParseOpts            []jwt.ParseOption
		endpointBuckets         []*regexp.Regexp
		limiter                 *rateLimiter
		params                  *ParamCheck

		// tellCaller is whether bascule's middleware writes the warnings
		// its approvers raise to the caller.  The CIDR approver is the
		// only source of warnings inside bascule, so its mode decides.
		tellCaller = true
	)

	if v.IsSet(basicConfigKey) && v.IsSet(jwtConfigKey) {
		return alice.Chain{}, nil, fmt.Errorf("`%s` and `%s` can't both be set", basicConfigKey, jwtConfigKey)
	} else if v.IsSet(basicConfigKey) {
		basicAllowed, err := decodeBasicCredentials(cfg.Basic)
		if err != nil {
			return alice.Chain{}, nil, err
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithBasic())
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(basicSchemeValidator))
		authorizerOpts = append(authorizerOpts,
			bascule.WithApproverFuncs(basicPasswordValidator(basicAllowed)))

		l.Info("inbound basic auth enabled", zap.Int("credentials", len(basicAllowed)))
	} else if v.IsSet(jwtConfigKey) {
		if kp == nil {
			return alice.Chain{}, nil, fmt.Errorf("`%s` must configure at least one key provider", clorthoConfigKey)
		}

		jwtParseOpts = append(jwtParseOpts, jwt.WithKeyProvider(kp))
		jwtp, err := basculejwt.NewTokenParser(jwtParseOpts...)
		if err != nil {
			return alice.Chain{}, nil, fmt.Errorf("error setting up JWT parser: %v", err)
		}

		authorizationParserOpts = append(authorizationParserOpts, basculehttp.WithScheme(basculehttp.SchemeBearer, jwtp))
		validatorOpts = append(validatorOpts, basculehttp.AsValidator(bearerSchemeValidator))

		approver, err := basculecaps.NewApprover(capabilityOptions(cfg.JWT)...)
		if err != nil {
			return alice.Chain{}, nil, fmt.Errorf("error setting up JWT capability checks: %v", err)
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

		if v.IsSet(cidrConfigKey) {
			cidr, err := newCIDRApprover(cfg.JWT.CIDR)
			if err != nil {
				return alice.Chain{}, nil, fmt.Errorf("error setting up JWT cidr capability checks: %v", err)
			}

			authorizerOpts = append(authorizerOpts, bascule.WithApprovers(cidr.approver))
			tellCaller = cidr.mode.warnings

			l.Info("inbound cidr capability checks enabled",
				zap.String("mode", cidr.mode.String()),
				zap.Strings("trustedProxies", cfg.JWT.CIDR.TrustedProxies))
		}

		if v.IsSet(rateConfigKey) {
			limiter, err = newRateLimiter(cfg.JWT.Rate, l, counter, warnings, endpointBuckets)
			if err != nil {
				return alice.Chain{}, nil, fmt.Errorf("error setting up JWT rate capability checks: %v", err)
			}

			l.Info("inbound rate capability checks enabled",
				zap.String("mode", limiter.mode.String()),
				zap.Strings("defaults", cfg.JWT.Rate.Defaults),
				zap.Int("overrides", len(cfg.JWT.Rate.Overrides)))
		}

		if v.IsSet(paramConfigKey) {
			params, err = newParamCheck(cfg.JWT.Param, l, counter, warnings, endpointBuckets)
			if err != nil {
				return alice.Chain{}, nil, fmt.Errorf("error setting up JWT parameter capability checks: %v", err)
			}

			l.Info("inbound parameter capability checks enabled",
				zap.String("mode", params.mode.String()),
				zap.Int("overrides", len(cfg.JWT.Param.Overrides)))
		}
	} else {
		return alice.Chain{}, nil, fmt.Errorf("either `%s` or `%s` must be set, but not both", basicConfigKey, jwtConfigKey)
	}

	tp, err := basculehttp.NewAuthorizationParser(authorizationParserOpts...)
	if err != nil {
		return alice.Chain{}, nil, fmt.Errorf("error setting up authorization parser: %v", err)
	}

	validatorOpts = append(validatorOpts, basculehttp.AsValidator(authPrincipalValidator))
	authorizerOpts = append(authorizerOpts,
		bascule.WithAuthorizeListeners(
			authorizerEvent{
				l:         l,
				counter:   counter,
				warnings:  warnings,
				endpoints: endpointBuckets}))
	middlewareOpts := make([]basculehttp.MiddlewareOption, 0, 3)
	if tellCaller {
		middlewareOpts = append(middlewareOpts, basculehttp.WithWarningHeader(WarningHeader))
	}

	middlewareOpts = append(middlewareOpts,
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
	auth, err := basculehttp.NewMiddleware(middlewareOpts...)
	if err != nil {
		return alice.Chain{}, nil, fmt.Errorf("failed to create auth middleware: %v", err)
	}

	// The limiter keeps state per principal, so it must run only once the
	// token has been verified: after auth, never before.
	constructors := []alice.Constructor{setLogger(l), auth.Then}
	if limiter != nil {
		constructors = append(constructors, limiter.handle)
	}

	return alice.New(constructors...), params, nil
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

// parseTrustedProxies parses the configured trusted proxy networks.  Each
// must be a network with a prefix length, as 10.0.0.0/8; a bare address is an
// error, so a single host is written /32 or /128.  Blank entries are skipped.
func parseTrustedProxies(cidrs []string) ([]netip.Prefix, error) {
	networks := make([]netip.Prefix, 0, len(cidrs))

	for i, c := range cidrs {
		c = strings.TrimSpace(c)
		if c == "" {
			continue
		}

		network, err := netip.ParsePrefix(c)
		if err != nil {
			return nil, fmt.Errorf("`%s[%d]`: expected a network such as 10.0.0.0/8: %w", trustedProxiesConfigKey, i, err)
		}

		networks = append(networks, network)
	}

	return networks, nil
}

// cidrCheck is the configured CIDR approver and the mode it runs in.
type cidrCheck struct {
	approver *basculehttp.CIDRApprover
	mode     checkMode
}

// newCIDRApprover builds the approver that holds a token's bearer to the
// networks its CIDR capabilities name.  The caller origin is resolved past
// the configured trusted proxies.
func newCIDRApprover(cfg cidrConfig) (cidrCheck, error) {
	trustedProxies, err := parseTrustedProxies(cfg.TrustedProxies)
	if err != nil {
		return cidrCheck{}, err
	}

	resolver, err := basculehttp.NewOriginResolver(basculehttp.WithTrustedProxies(trustedProxies...))
	if err != nil {
		return cidrCheck{}, err
	}

	mode, err := parseMode(cfg.Mode, cidrConfigKey,
		ModePermissive, ModePermissiveWithWarnings, ModeEnforce, ModeRequired,
		ModePermissiveRequired, ModePermissiveRequiredWithWarnings)
	if err != nil {
		return cidrCheck{}, err
	}

	opts := []basculehttp.CIDRApproverOption{
		basculehttp.WithCIDRPrefixes(cfg.Prefixes...),
		basculehttp.WithOriginResolver(resolver),
		cidrModeOption(mode),
	}

	if cfg.CacheSize > 0 {
		opts = append(opts, basculehttp.WithCIDRCacheSize(cfg.CacheSize))
	}

	approver, err := basculehttp.NewCIDRApprover(opts...)
	if err != nil {
		return cidrCheck{}, err
	}

	return cidrCheck{approver: approver, mode: mode}, nil
}

// cidrModeOption maps a mode onto the approver's option for it.
func cidrModeOption(mode checkMode) basculehttp.CIDRApproverOption {
	switch {
	case mode.required && mode.permissive:
		return basculehttp.WithCIDRPermissiveRequired()
	case mode.required:
		return basculehttp.WithCIDRRequired()
	case mode.permissive:
		return basculehttp.WithCIDRPermissiveCorrectIfPresent()
	default:
		return basculehttp.WithCIDRCorrectIfPresent()
	}
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
