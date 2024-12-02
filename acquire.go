// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"go.uber.org/fx"
)

// ErrEmptyCredentials is returned whenever an Acquirer is attempted to
// be built with empty credentials.
// Use DefaultAcquirer for such no-op use case.
var ErrEmptyCredentials = errors.New("empty credentials are not valid")

// TODO: need to move to ancla once we have all ancla changes made
type Acquirer interface {
	Acquire() (string, error)
}

type Auth struct {
	JWT   RemoteBearerTokenAcquirerOptions
	Basic string
}

func NewAcquirer(auth Auth) (Acquirer, error) {
	if !isEmpty(auth.JWT) {
		return NewRemoteBearerTokenAcquirer(auth.JWT)
	} else {
		return NewFixedAuthAcquirer("")
	}
}

// DefaultAcquirer is a no-op Acquirer.
type DefaultAcquirer struct{}

// Acquire returns the zero values of the return types.
func (d *DefaultAcquirer) Acquire() (string, error) {
	return "", nil
}

type FixedValueAcquirer struct {
	authValue string
}

// NewFixedAuthAcquirer returns a FixedValueAcquirer with the given authValue.
func NewFixedAuthAcquirer(authValue string) (*FixedValueAcquirer, error) {
	if authValue != "" {
		return &FixedValueAcquirer{
			authValue: authValue}, nil
	}
	return nil, ErrEmptyCredentials
}

func (f *FixedValueAcquirer) Acquire() (string, error) {
	return f.authValue, nil
}

// RemoteBearerTokenAcquirer implements Acquirer and fetches the tokens from a remote location with caching strategy.
type RemoteBearerTokenAcquirer struct {
	options                RemoteBearerTokenAcquirerOptions
	authValue              string
	authValueExpiration    time.Time
	httpClient             *http.Client
	nonExpiringSpecialCase time.Time
	lock                   sync.RWMutex
}

type RemoteBearerTokenAcquirerOptions struct {
	AuthURL        string            `json:"authURL"`
	Timeout        time.Duration     `json:"timeout"`
	Buffer         time.Duration     `json:"buffer"`
	RequestHeaders map[string]string `json:"requestHeaders"`

	GetToken      TokenParser
	GetExpiration ParseExpiration
}

// NewRemoteBearerTokenAcquirer returns a RemoteBearerTokenAcquirer configured with the given options.
func NewRemoteBearerTokenAcquirer(options RemoteBearerTokenAcquirerOptions) (*RemoteBearerTokenAcquirer, error) {
	if options.GetToken == nil {
		options.GetToken = DefaultTokenParser
	}

	if options.GetExpiration == nil {
		options.GetExpiration = DefaultExpirationParser
	}

	// TODO: we should inject timeout and buffer defaults values as well.

	return &RemoteBearerTokenAcquirer{
		options:             options,
		authValueExpiration: time.Now(),
		httpClient: &http.Client{
			Timeout: options.Timeout,
		},
		nonExpiringSpecialCase: time.Unix(0, 0),
	}, nil
}

// Acquire provides the cached token or, if it's near its expiry time, contacts
// the server for a new token to cache.
func (a *RemoteBearerTokenAcquirer) Acquire() (string, error) {
	a.lock.RLock()
	if time.Now().Add(a.options.Buffer).Before(a.authValueExpiration) {
		defer a.lock.RUnlock()
		return a.authValue, nil
	}
	a.lock.RUnlock()
	a.lock.Lock()
	defer a.lock.Unlock()

	req, err := http.NewRequest("GET", a.options.AuthURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create new request for Bearer: %v", err)
	}

	for key, value := range a.options.RequestHeaders {
		req.Header.Set(key, value)
	}

	resp, errHTTP := a.httpClient.Do(req)
	if errHTTP != nil {
		return "", fmt.Errorf("error making request to '%v' to acquire bearer token: %v",
			a.options.AuthURL, errHTTP)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("received non 200 code acquiring Bearer: code %v", resp.Status)
	}

	respBody, errRead := io.ReadAll(resp.Body)
	if errRead != nil {
		return "", fmt.Errorf("error reading HTTP response body: %v", errRead)
	}

	token, err := a.options.GetToken(respBody)
	if err != nil {
		return "", fmt.Errorf("error parsing bearer token from http response body: %v", err)
	}
	expiration, err := a.options.GetExpiration(respBody)
	if err != nil {
		return "", fmt.Errorf("error parsing bearer token expiration from http response body: %v", err)
	}

	a.authValue, a.authValueExpiration = "Bearer "+token, expiration
	return a.authValue, nil
}

// SimpleBearer defines the field name mappings used by the default bearer token and expiration parsers.
type SimpleBearer struct {
	ExpiresInSeconds float64 `json:"expires_in"`
	Token            string  `json:"serviceAccessToken"`
}

// TokenParser defines the function signature of a bearer token extractor from a payload.
type TokenParser func([]byte) (string, error)

// ParseExpiration defines the function signature of a bearer token expiration date extractor.
type ParseExpiration func([]byte) (time.Time, error)

// DefaultTokenParser extracts a bearer token as defined by a SimpleBearer in a payload.
func DefaultTokenParser(data []byte) (string, error) {
	var bearer SimpleBearer

	if errUnmarshal := json.Unmarshal(data, &bearer); errUnmarshal != nil {
		return "", fmt.Errorf("unable to parse bearer token: %w", errUnmarshal)
	}
	return bearer.Token, nil
}

// DefaultExpirationParser extracts a bearer token expiration date as defined by a SimpleBearer in a payload.
func DefaultExpirationParser(data []byte) (time.Time, error) {
	var bearer SimpleBearer

	if errUnmarshal := json.Unmarshal(data, &bearer); errUnmarshal != nil {
		return time.Time{}, fmt.Errorf("unable to parse bearer token expiration: %w", errUnmarshal)
	}
	return time.Now().Add(time.Duration(bearer.ExpiresInSeconds) * time.Second), nil
}

func isEmpty(options RemoteBearerTokenAcquirerOptions) bool {
	return len(options.AuthURL) < 1 || options.Buffer == 0 || options.Timeout == 0
}

type AcquirerIn struct {
	fx.In
	Auth Auth
}

func ProvideAcquirer() fx.Option {
	return fx.Provide(
		func(in AcquirerIn) (Acquirer, error) {
			return NewAcquirer(in.Auth)
		},
	)
}
