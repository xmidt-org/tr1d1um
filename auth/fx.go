// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"crypto/tls"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/justinas/alice"
	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/xmidt-org/ancla/auth"
	"github.com/xmidt-org/arrange"
	"github.com/xmidt-org/clortho"
	"github.com/xmidt-org/clortho/clorthofx"
	"github.com/xmidt-org/touchstone"
	"go.uber.org/fx"
	"go.uber.org/multierr"
	"go.uber.org/zap"
)

const module = "tr1d1um.auth"

const (
	// defaultClorthoClientTimeout bounds an exchange with a key server when
	// the configuration gives no timeout.  clortho supplies no client of its
	// own, and a client with no timeout lets a server that stops answering
	// hold every token lookup that waits on it.
	defaultClorthoClientTimeout = 30 * time.Second

	// defaultClorthoDialerTimeout bounds connection establishment to a key
	// server when the configuration gives no timeout.
	defaultClorthoDialerTimeout = 5 * time.Second
)

// Provide wires inbound authentication and outbound request decoration.
//
// The configuration is consulted here, before the fx graph is built, because
// clorthofx refuses an empty configuration: a deployment that accepts only
// basic credentials has no keys to fetch, so the key providers are wired only
// when JWT authentication is configured.
func Provide(v *viper.Viper) fx.Option {
	opts := []fx.Option{
		provideMetrics(),
		fx.Provide(
			arrange.UnmarshalKey(inboundConfigKey, inboundConfig{}),
			arrange.UnmarshalKey(outboundConfigKey, outboundConfig{}),
			fx.Annotate(
				provideDecoratorsParseOpts,
				fx.ParamTags(`optional:"true"`),
				fx.ResultTags(`group:"jwt_parse_options,flatten"`),
			),
			provideDecorators,
			provideMiddleware,
		),
	}

	if v.IsSet(jwtConfigKey) {
		opts = append(opts,
			clorthofx.Provide(),
			fx.Provide(
				arrange.UnmarshalKey(clorthoConfigKey, clorthoConfig{}),
				newClorthoConfig,
			),
		)
	}

	return fx.Module(module, opts...)
}

func provideMetrics() fx.Option {
	return touchstone.CounterVec(
		prometheus.CounterOpts{
			Name: AuthCapabilityCheckCount,
			Help: "Counter for the capability check, providing outcome information by client, partner, and endpoint",
		},
		OutcomeLabel,
		ReasonLabel,
		ClientIDLabel,
		PartnerIDLabel,
		EndpointLabel,
		MethodLabel,
	)
}

// clorthoConfig is the auth.inbound.JWT.Clortho section: the key providers to
// build and the HTTP client they reach key servers with.
//
// The provider lists are clortho's own configuration types.  They carry no
// struct tags, so a key in the file is the field name, matched without regard
// to case: keySets, sources, uri, refreshInterval, and so on.  The one field a
// file cannot hold, each source's *http.Client, is filled in from Client by
// newClorthoConfig.
type clorthoConfig struct {
	// Client configures the HTTP client given to every http or https key
	// source and to every per-key provider.
	Client clorthoClientConfig

	// KeySets, PerKeys and Fixed are the providers to build; see
	// clorthofx.Config.  At least one, of any kind, is required.
	KeySets []clortho.KeySetConfig
	PerKeys []clortho.PerKeyConfig
	Fixed   []clortho.FixedKeyConfig
}

// clorthoClientConfig configures the HTTP client used to fetch keys.
type clorthoClientConfig struct {
	// ClientTimeout bounds a whole exchange with a key server, including
	// reading the body.  Defaults to 30s.
	ClientTimeout time.Duration

	// NetDialerTimeout bounds connection establishment.  Defaults to 5s.
	NetDialerTimeout time.Duration
}

// newClorthoConfig hands clortho the configured providers, with the HTTP
// client set on every source that needs one.  A file source takes no client,
// and clortho rejects one that is given one.
func newClorthoConfig(cfg clorthoConfig) clorthofx.Config {
	client := newClorthoHTTPClient(cfg.Client)

	out := clorthofx.Config{
		KeySets: cfg.KeySets,
		PerKeys: cfg.PerKeys,
		Fixed:   cfg.Fixed,
	}

	for i := range out.KeySets {
		for j := range out.KeySets[i].Sources {
			source := &out.KeySets[i].Sources[j]
			if isHTTPSource(source.URI) {
				source.Client = client
			}
		}
	}

	for i := range out.PerKeys {
		out.PerKeys[i].Client = client
	}

	return out
}

// isHTTPSource reports whether a key set source is reached over http or
// https, as opposed to being a file path or file URI.
func isHTTPSource(uri string) bool {
	u, err := url.Parse(uri)
	if err != nil {
		return false
	}

	return u.Scheme == "http" || u.Scheme == "https"
}

// newClorthoHTTPClient builds the client clortho fetches keys with.  Keys come
// from the configured URI and nowhere else, so redirects are not followed.
func newClorthoHTTPClient(cfg clorthoClientConfig) *http.Client {
	if cfg.ClientTimeout <= 0 {
		cfg.ClientTimeout = defaultClorthoClientTimeout
	}
	if cfg.NetDialerTimeout <= 0 {
		cfg.NetDialerTimeout = defaultClorthoDialerTimeout
	}

	return &http.Client{
		Timeout: cfg.ClientTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout: cfg.NetDialerTimeout,
			}).DialContext,
			TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12},
		},
	}
}

type chainIn struct {
	fx.In

	Cfg    inboundConfig
	V      *viper.Viper
	Logger *zap.Logger

	// KeyProvider supplies JWT verification keys.  It is absent when JWT
	// authentication is not configured.
	KeyProvider jws.KeyProvider `optional:"true"`

	Counter *prometheus.CounterVec `name:"auth_capability_check"`
}

type chainOut struct {
	fx.Out

	Middleware alice.Chain `name:"auth_chain"`
}

func provideMiddleware(in chainIn) (chainOut, error) {
	middle, err := NewMiddleware(in.Cfg, in.V, in.KeyProvider, in.Logger, in.Counter)

	return chainOut{Middleware: middle}, err
}

type decoratorIn struct {
	fx.In

	Cfg       outboundConfig
	V         *viper.Viper
	PraseOpts []jwt.ParseOption `group:"jwt_parse_options"`
}

func provideDecorators(in decoratorIn) (Decorator, auth.Decorator, error) {
	fod, err := NewDecorator(in.Cfg.Fanout, in.V, fanoutJWTConfigKey, fanoutBasicConfigKey, in.PraseOpts...)
	wd, err1 := NewDecorator(in.Cfg.Webhook, in.V, webhookJWTConfigKey, webhookBascicConfigKey, in.PraseOpts...)

	return fod, wd, multierr.Append(err, err1)
}

// provideDecoratorsParseOpts returns the options an outbound bearer decorator
// parses an acquired token with.  The token is read only for its expiration.
// When inbound JWT authentication is configured its key provider verifies the
// acquired token too; otherwise there is nothing to verify it against and it
// is parsed unverified, as the previous token acquirer did.
func provideDecoratorsParseOpts(kp jws.KeyProvider) []jwt.ParseOption {
	if kp == nil {
		return []jwt.ParseOption{jwt.WithVerify(false)}
	}

	return []jwt.ParseOption{jwt.WithKeyProvider(kp)}
}
