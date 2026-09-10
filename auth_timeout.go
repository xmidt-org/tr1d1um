// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"time"

	"github.com/xmidt-org/clortho"
)

const defaultJWKSResolveTimeout = 5 * time.Second

// timeoutResolver wraps a clortho.Resolver and enforces a deadline on each
// Resolve call so waiters cannot block forever when JWKS fetch hangs.
type timeoutResolver struct {
	delegate clortho.Resolver
	timeout  time.Duration
}

// Resolve fetches a signing key by key ID, cancelling the call if it exceeds
// the configured timeout (used during JWT validation).
func (tr *timeoutResolver) Resolve(ctx context.Context, keyID string) (clortho.Key, error) {
	if tr.timeout <= 0 {
		return tr.delegate.Resolve(ctx, keyID)
	}

	resolveCtx, cancel := context.WithTimeout(ctx, tr.timeout)
	defer cancel()

	return tr.delegate.Resolve(resolveCtx, keyID)
}

// AddListener forwards resolve-event listeners to the underlying resolver.
func (tr *timeoutResolver) AddListener(l clortho.ResolveListener) clortho.CancelListenerFunc {
	return tr.delegate.AddListener(l)
}

// jwksResolveTimeout returns the JWKS fetch/resolve deadline from config, or
// the default when unset.
func jwksResolveTimeout(v JWTValidator) time.Duration {
	if v.Config.Resolve.Timeout > 0 {
		return v.Config.Resolve.Timeout
	}

	return defaultJWKSResolveTimeout
}

// provideJWKSFetcherOptions builds clortho fetcher options that apply an HTTP
// client timeout to JWKS downloads from Themis (http/https).
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

// newTimedResolver creates a clortho Resolver with timed JWKS HTTP fetches and
// a Resolve-level deadline wrapper for JWT auth.
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

// decorateResolverWithTimeout wraps resolver so each Resolve call is bounded
// by jwksResolveTimeout.
func decorateResolverWithTimeout(r clortho.Resolver, v JWTValidator) clortho.Resolver {
	return &timeoutResolver{
		delegate: r,
		timeout:  jwksResolveTimeout(v),
	}
}
