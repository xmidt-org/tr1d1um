// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/arrange/arrangehttp"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"
)

const testDevicePath = "/api/v3/device/mac:112233445566/config"

// basic builds an Authorization header value for basic credentials.
func basic(user, password string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+password))
}

// listenAddresses records where each server ended up listening, since the
// test binds every server to a port the operating system picks.
type listenAddresses struct {
	lock  sync.Mutex
	addrs map[string]string
}

// capture provides a listener middleware for the named server that records
// its bind address.
func (la *listenAddresses) capture(server string) fx.Option {
	return fx.Provide(
		fx.Annotate(
			func() arrangehttp.ListenerMiddleware {
				return func(l net.Listener) net.Listener {
					la.lock.Lock()
					defer la.lock.Unlock()
					la.addrs[server] = l.Addr().String()

					return l
				}
			},
			fx.ResultTags(fmt.Sprintf(`group:"%s.listener.middleware"`, server)),
		),
	)
}

func (la *listenAddresses) url(t *testing.T, server, path string) string {
	t.Helper()

	la.lock.Lock()
	defer la.lock.Unlock()

	addr, ok := la.addrs[server]
	require.True(t, ok, "the server %s never started listening", server)

	return "http://" + addr + path
}

// response is the part of an HTTP response the test looks at.
type response struct {
	status int
	header http.Header
	body   string
}

func get(t *testing.T, url, authorization string) response {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, url, nil)
	require.NoError(t, err)
	if authorization != "" {
		req.Header.Set("Authorization", authorization)
	}

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	return response{status: resp.StatusCode, header: resp.Header, body: string(body)}
}

// TestApp starts the whole application from the sample configuration and
// checks that each server answers as configured: its routes, its response
// headers, its request metrics, and the authentication in front of the API.
func TestApp(t *testing.T) {
	// Stands in for Argus, so the webhook listener has something to poll.
	argus := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	defer argus.Close()

	v := viper.New()
	for key, value := range defaults {
		v.SetDefault(key, value)
	}
	v.SetConfigFile("tr1d1um.yaml")
	require.NoError(t, v.ReadInConfig())

	// Merged rather than set: a viper override of one nested key hides its
	// siblings, which would drop the configured response headers.
	anyPort := map[string]any{"address": "127.0.0.1:0"}
	require.NoError(t, v.MergeConfigMap(map[string]any{
		"servers": map[string]any{
			"primary":   anyPort,
			"alternate": anyPort,
			"health":    anyPort,
			"metrics":   anyPort,
			"pprof":     anyPort,
		},
		"webhook": map[string]any{
			"client": map[string]any{"storeBaseURL": argus.URL},
		},
	}))

	servers := []string{primaryServer, alternateServer, healthServer, metricsServer, pprofServer}
	listening := &listenAddresses{addrs: make(map[string]string)}

	options := make([]fx.Option, 0, 1+len(servers))
	options = append(options, newApp(v, zap.NewNop()))
	for _, server := range servers {
		options = append(options, listening.capture(server))
	}

	app := fxtest.New(t, options...)
	app.RequireStart()
	defer app.RequireStop()

	// the credential the sample configuration accepts, and one it does not
	configured, unknown := basic("user", "pass"), basic("nope", "nope")

	t.Run("health", func(t *testing.T) {
		got := get(t, listening.url(t, healthServer, "/health"), "")
		assert.Equal(t, http.StatusOK, got.status)
		assert.Equal(t, "tr1d1um", got.header.Get("X-Midt-Server"))

		assert.Equal(t, http.StatusNotFound, get(t, listening.url(t, healthServer, "/nope"), "").status)
	})

	t.Run("pprof", func(t *testing.T) {
		assert.Equal(t, http.StatusOK, get(t, listening.url(t, pprofServer, "/debug/pprof/"), "").status)
		assert.Equal(t, http.StatusOK, get(t, listening.url(t, pprofServer, "/debug/pprof/cmdline"), "").status)
	})

	for _, server := range []string{primaryServer, alternateServer} {
		t.Run(server, func(t *testing.T) {
			// no credentials
			got := get(t, listening.url(t, server, testDevicePath+"?names=x"), "")
			assert.Equal(t, http.StatusUnauthorized, got.status)
			assert.Equal(t, "tr1d1um", got.header.Get("X-Midt-Server"))

			// a credential that is not in the configured list
			got = get(t, listening.url(t, server, testDevicePath+"?names=x"), unknown)
			assert.Equal(t, http.StatusUnauthorized, got.status)

			// the configured credential reaches the handler, which rejects
			// the request for having no names before calling anything
			got = get(t, listening.url(t, server, testDevicePath), configured)
			assert.Equal(t, http.StatusBadRequest, got.status)

			// the previous api version is served, an unknown one is not
			got = get(t, listening.url(t, server, "/api/v2/device/mac:112233445566/config"), configured)
			assert.Equal(t, http.StatusBadRequest, got.status)
			got = get(t, listening.url(t, server, "/api/v1/device/mac:112233445566/config"), configured)
			assert.Equal(t, http.StatusNotFound, got.status)

			// the webhook routes sit behind the same authentication
			got = get(t, listening.url(t, server, "/api/v3/hooks"), "")
			assert.Equal(t, http.StatusUnauthorized, got.status)
		})
	}

	t.Run("metrics", func(t *testing.T) {
		got := get(t, listening.url(t, metricsServer, "/metrics"), "")
		require.Equal(t, http.StatusOK, got.status)

		// The metrics middleware wraps each whole server, so it counts the
		// requests above by server, including the one that matched no route.
		for _, want := range []string{
			`server_request_count{code="401",method="GET",server="server_primary"}`,
			`server_request_count{code="400",method="GET",server="server_primary"}`,
			`server_request_count{code="404",method="GET",server="server_primary"}`,
			`server_request_count{code="401",method="GET",server="server_alternate"}`,
			`server_request_count{code="200",method="GET",server="server_health"}`,
			`server_request_count{code="404",method="GET",server="server_health"}`,
		} {
			assert.Contains(t, got.body, want)
		}

		// the application's own metrics are registered
		assert.Contains(t, got.body, "auth_capability_check")
	})
}
