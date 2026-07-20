// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/justinas/alice"
	"github.com/xmidt-org/clortho"
)

const (
	defaultJWKSResolveTimeout = 5 * time.Second
	// defaultAuthRequestTimeout must stay above xmidtClientTimeout (135s) so
	// legitimate device/config requests are not cancelled early.
	defaultAuthRequestTimeout = 150 * time.Second
)

type timeoutResolver struct {
	delegate clortho.Resolver
	timeout  time.Duration
}

func (tr *timeoutResolver) Resolve(ctx context.Context, keyID string) (clortho.Key, error) {
	if tr.timeout <= 0 {
		return tr.delegate.Resolve(ctx, keyID)
	}

	resolveCtx, cancel := context.WithTimeout(ctx, tr.timeout)
	defer cancel()

	return tr.delegate.Resolve(resolveCtx, keyID)
}

func (tr *timeoutResolver) AddListener(l clortho.ResolveListener) clortho.CancelListenerFunc {
	return tr.delegate.AddListener(l)
}

func jwksResolveTimeout(v JWTValidator) time.Duration {
	if v.Config.Resolve.Timeout > 0 {
		return v.Config.Resolve.Timeout
	}

	return defaultJWKSResolveTimeout
}

func authRequestTimeout(v JWTValidator) time.Duration {
	if v.AuthRequestTimeout > 0 {
		return v.AuthRequestTimeout
	}

	return defaultAuthRequestTimeout
}

func provideJWKSFetcherOptions(v JWTValidator) ([]clortho.FetcherOption, error) {
	timeout := jwksResolveTimeout(v)

	httpLoader := clortho.HTTPLoader{
		Client: &http.Client{
			Timeout: timeout,
		},
		Timeout: timeout,
	}

	loader, err := clortho.NewLoader(
		clortho.WithSchemes(httpLoader, "http", "https"),
	)
	if err != nil {
		return nil, err
	}

	return []clortho.FetcherOption{clortho.WithLoader(loader)}, nil
}

func newTimedResolver(v JWTValidator) (clortho.Resolver, error) {
	fetcherOptions, err := provideJWKSFetcherOptions(v)
	if err != nil {
		return nil, err
	}

	fetcher, err := clortho.NewFetcher(fetcherOptions...)
	if err != nil {
		return nil, err
	}

	resolver, err := clortho.NewResolver(
		clortho.WithConfig(v.Config),
		clortho.WithFetcher(fetcher),
	)
	if err != nil {
		return nil, err
	}

	return decorateResolverWithTimeout(resolver, v), nil
}

func decorateResolverWithTimeout(r clortho.Resolver, v JWTValidator) clortho.Resolver {
	return &timeoutResolver{
		delegate: r,
		timeout:  jwksResolveTimeout(v),
	}
}

func provideRequestTimeoutMiddleware(v JWTValidator) alice.Constructor {
	timeout := authRequestTimeout(v)
	return requestTimeoutMiddleware(timeout)
}

func requestTimeoutMiddleware(timeout time.Duration) alice.Constructor {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), timeout)
			defer cancel()

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
