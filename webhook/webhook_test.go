// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package webhook

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/xmidt-org/candlelight"
)

func TestNewListenerOpts(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		wantLen  int
		wantErr  bool
	}{
		{name: "default", wantLen: 1},
		{name: "configured", interval: "30s", wantLen: 2},
		{name: "invalid", interval: "soon", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			opts, err := NewListenerOpts(listnerConfig{PullInterval: tc.interval})
			if tc.wantErr {
				require.Error(t, err)
				assert.Nil(t, opts)

				return
			}

			require.NoError(t, err)
			assert.Len(t, opts, tc.wantLen)
		})
	}
}

func TestNewHTTPClient(t *testing.T) {
	tracing, err := candlelight.New(candlelight.Config{})
	require.NoError(t, err)

	tests := []struct {
		name    string
		cfg     clientConfig
		timeout time.Duration
	}{
		{name: "default timeout", timeout: 50 * time.Second},
		{name: "negative timeout uses default", cfg: clientConfig{ClientTimeout: -time.Second}, timeout: 50 * time.Second},
		{name: "configured timeout", cfg: clientConfig{ClientTimeout: 3 * time.Second}, timeout: 3 * time.Second},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client := NewHTTPClient(tc.cfg, tracing)
			require.NotNil(t, client)
			assert.Equal(t, tc.timeout, client.Timeout)
			assert.NotNil(t, client.Transport)
			assert.NotSame(t, http.DefaultTransport, client.Transport)
		})
	}
}

func TestNewClientOpts(t *testing.T) {
	tracing, err := candlelight.New(candlelight.Config{})
	require.NoError(t, err)

	opts := NewClientOpts(clientConfig{
		StoreBaseURL: "https://argus.example.com",
		StoreAPIPath: "/api/v1/store",
		Bucket:       "hooks",
	}, tracing, nil)
	assert.Len(t, opts, 6)
}
