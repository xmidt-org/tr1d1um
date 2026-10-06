// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/spf13/viper"
	"github.com/xmidt-org/bascule/basculehttp"
)

const (
	outboundConfigKey      = "auth.outbound"
	fanoutJWTConfigKey     = "auth.outbound.fanout.JWT"
	fanoutBasicConfigKey   = "auth.outbound.fanout.Basic"
	webhookJWTConfigKey    = "auth.outbound.webhook.JWT"
	webhookBascicConfigKey = "auth.outbound.webhook.Basic"
)

var (
	ErrEmptyAuthCredentials = errors.New("empty credentials are not valid")
	ErrEmptyAuthURL         = errors.New("empty auth url is not valid")
)

// Decorator decorates http requests with authorization header(s).
type Decorator interface {
	// Decorate decorates the given http request with authorization header(s).
	Decorate(ctx context.Context, req *http.Request) error
}

type outboundConfig struct {
	Fanout  decoratorConfig
	Webhook decoratorConfig
}

type decoratorConfig struct {
	JWT   bearerDecoratorConfig
	Basic string
}

type bearerDecoratorConfig struct {
	AuthURL        string
	PayloadField   string
	Timeout        time.Duration
	Buffer         time.Duration
	RequestHeaders map[string]string
}

func NewDecorator(cfg decoratorConfig, v *viper.Viper, jwtKey, basicKey string, opts ...jwt.ParseOption) (Decorator, error) {
	if v.IsSet(jwtKey) && v.IsSet(basicKey) {
		return nil, fmt.Errorf("`%s` and `%s` can't both be set", jwtKey, basicKey)
	}

	if v.IsSet(jwtKey) {
		if len(cfg.JWT.RequestHeaders) == 0 {
			return nil, fmt.Errorf("%w: `%s`", ErrEmptyAuthCredentials, jwtKey)
		} else if len(cfg.JWT.AuthURL) == 0 {
			return nil, fmt.Errorf("%w: `%s`", ErrEmptyAuthURL, jwtKey)
		}

		return &bearerDecorator{
			exp:          time.Now(),
			url:          cfg.JWT.AuthURL,
			payloadField: cfg.JWT.PayloadField,
			buffer:       cfg.JWT.Buffer,
			headers:      cfg.JWT.RequestHeaders,
			httpClient: &http.Client{
				Timeout: cfg.JWT.Timeout,
			},
			parseOpts: opts,
		}, nil
	} else if v.IsSet(basicKey) {
		if len(cfg.Basic) == 0 {
			return nil, fmt.Errorf("%w: `%s`", ErrEmptyAuthCredentials, basicKey)
		}

		return &basicDecorator{value: cfg.Basic}, nil
	}

	return nil, fmt.Errorf("either `%s` or `%s` must be set, but not both", jwtKey, basicKey)
}

// bearerDecorator implements Decorator and fetches the tokens from a remote location with caching strategy.
type bearerDecorator struct {
	token        jwt.Token
	exp          time.Time
	cache        string
	url          string
	payloadField string
	buffer       time.Duration
	headers      map[string]string
	httpClient   *http.Client
	parseOpts    []jwt.ParseOption
	lock         sync.RWMutex
}

// Decorate decorates the given http request with authorization header(s) using the cached token or, if it's near its expiry time, contacts
// the server for a new token to cache.
func (bearer *bearerDecorator) Decorate(ctx context.Context, req *http.Request) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	bearer.lock.RLock()
	if time.Now().Add(bearer.buffer).Before(bearer.exp) && len(bearer.cache) != 0 {
		defer bearer.lock.RUnlock()
		req.Header.Set(basculehttp.DefaultAuthorizationHeader, bearer.cache)

		return nil
	}

	bearer.lock.RUnlock()
	bearer.lock.Lock()
	defer bearer.lock.Unlock()

	req, err := http.NewRequest("GET", bearer.url, nil)
	if err != nil {
		return fmt.Errorf("failed to create new request for Bearer: %v", err)
	}

	for key, value := range bearer.headers {
		req.Header.Set(key, value)
	}

	resp, err := bearer.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("error making request to '%v' to acquire bearer token: %v",
			bearer.url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("received non 200 code acquiring Bearer: code %v", resp.Status)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("error reading HTTP response body: %v", body)
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return fmt.Errorf("error parsing the http response body from `%s`: %v", bearer.url, err)
	}

	v, ok := payload[bearer.payloadField]
	if !ok {
		return fmt.Errorf("expected the http response payload field `%s` from `%s` to exist and be nonempty", bearer.payloadField, bearer.url)
	}

	stoken, ok := v.(string)
	if !ok {
		return fmt.Errorf("couldn't convert the token, from the http response payload field `%s` from `%s`, to a string: unexpected type %T", bearer.payloadField, bearer.url, v)
	}

	token, err := jwt.ParseString(stoken, bearer.parseOpts...)
	if err != nil {
		return fmt.Errorf("error parsing bearer token from http response body: %v", err)
	}

	exp, ok := token.Expiration()
	if !ok {
		return errors.New("bearer token must contain an expieration")
	}

	bearer.exp = exp
	bearer.token = token
	bearer.cache = fmt.Sprintf("%s %s", basculehttp.SchemeBearer, stoken)
	req.Header.Set(basculehttp.DefaultAuthorizationHeader, bearer.cache)

	return nil
}

// basicDecorator implements Decorator with a constant authorization value.
type basicDecorator struct {
	value string
}

func (basic *basicDecorator) Decorate(ctx context.Context, req *http.Request) error {
	req.Header.Set(basculehttp.DefaultAuthorizationHeader, fmt.Sprintf("%s %s", basculehttp.SchemeBasic, basic.value))

	return nil
}
