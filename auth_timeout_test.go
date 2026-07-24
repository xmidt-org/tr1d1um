// SPDX-FileCopyrightText: 2022 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/clortho"
)

type blockingResolver struct {
	started chan struct{}
	release chan struct{}
}

func (br *blockingResolver) Resolve(ctx context.Context, _ string) (clortho.Key, error) {
	close(br.started)
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-br.release:
		return nil, errors.New("released")
	}
}

func (br *blockingResolver) AddListener(_ clortho.ResolveListener) clortho.CancelListenerFunc {
	return func() {}
}

func TestTimeoutResolver(t *testing.T) {
	t.Parallel()

	blocker := &blockingResolver{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
	resolver := &timeoutResolver{
		delegate: blocker,
		timeout:  50 * time.Millisecond,
	}

	done := make(chan error, 1)
	go func() {
		_, err := resolver.Resolve(context.Background(), "current")
		done <- err
	}()

	<-blocker.started
	err := <-done
	require.Error(t, err)
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}

func TestJWKSResolveTimeoutDefaults(t *testing.T) {
	t.Parallel()

	assert.Equal(t, defaultJWKSResolveTimeout, jwksResolveTimeout(JWTValidator{}))
	assert.Equal(t, 3*time.Second, jwksResolveTimeout(JWTValidator{
		Config: clortho.Config{Resolve: clortho.ResolveConfig{Timeout: 3 * time.Second}},
	}))
}

func TestProvideJWKSFetcherOptions(t *testing.T) {
	t.Parallel()

	options, err := provideJWKSFetcherOptions(JWTValidator{
		Config: clortho.Config{
			Resolve: clortho.ResolveConfig{
				Timeout: 2 * time.Second,
			},
		},
	})
	require.NoError(t, err)
	require.Len(t, options, 1)
}

func TestNewTimedResolver(t *testing.T) {
	t.Parallel()

	resolver, err := newTimedResolver(JWTValidator{
		Config: clortho.Config{
			Resolve: clortho.ResolveConfig{
				Template: "https://keys.example/{keyID}",
				Timeout:  2 * time.Second,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, resolver)
}
