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
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"
	"go.uber.org/zap"

	"github.com/xmidt-org/arrange/arrangehttp"
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

func TestConfigureXmidtClientTimeout(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		got := configureXmidtClientTimeout(XmidtClientTimeoutConfigIn{})
		assert.Equal(t, httpClientTimeout{
			ClientTimeout:    135 * time.Second,
			NetDialerTimeout: 5 * time.Second,
			RequestTimeout:   129 * time.Second,
		}, got)
	})

	t.Run("configured values are kept", func(t *testing.T) {
		want := httpClientTimeout{
			ClientTimeout:    time.Second,
			NetDialerTimeout: 2 * time.Second,
			RequestTimeout:   3 * time.Second,
		}
		got := configureXmidtClientTimeout(XmidtClientTimeoutConfigIn{XmidtClientTimeout: want})
		assert.Equal(t, want, got)
	})
}

// writeAppConfig writes the sample configuration, with every server on a free
// port, the webhook store at storeURL and the log written to logFile, merged
// with overrides, to a file and returns its path.
func writeAppConfig(t *testing.T, storeURL, logFile string, overrides map[string]any) string {
	t.Helper()

	v := viper.New()
	v.SetConfigFile("tr1d1um.yaml")
	require.NoError(t, v.ReadInConfig())

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
			"client": map[string]any{"storeBaseURL": storeURL},
		},
		"logging": map[string]any{
			"outputPaths":   []string{logFile},
			"encoderConfig": map[string]any{"messageKey": "msg"},
		},
	}))
	require.NoError(t, v.MergeConfigMap(overrides))

	path := filepath.Join(t.TempDir(), "tr1d1um.yaml")
	require.NoError(t, v.WriteConfigAs(path))

	return path
}

// newStore stands in for Argus, so the webhook listener has something to poll.
func newStore(t *testing.T) *httptest.Server {
	t.Helper()

	store := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("[]"))
	}))
	t.Cleanup(store.Close)

	return store
}

func TestTr1d1um_ExitCodes(t *testing.T) {
	t.Run("no configuration", func(t *testing.T) {
		if _, err := os.Stat("/etc/" + applicationName); err == nil {
			t.Skip("/etc/" + applicationName + " exists on this machine")
		}
		isolateSearchPath(t)

		assert.Equal(t, 1, tr1d1um([]string{applicationName}))
	})

	// pflag prints the usage; asking for it is not an error
	for _, flag := range []string{"-h", "--help"} {
		t.Run(flag, func(t *testing.T) {
			assert.Equal(t, 0, tr1d1um([]string{applicationName, flag}))
		})
	}

	t.Run("unknown flag", func(t *testing.T) {
		assert.Equal(t, 1, tr1d1um([]string{applicationName, "--nope"}))
	})

	t.Run("invalid configuration", func(t *testing.T) {
		logFile := filepath.Join(t.TempDir(), "tr1d1um.log")
		path := writeAppConfig(t, newStore(t).URL, logFile, map[string]any{
			"WRPSource": "not a locator",
		})

		// the configuration reads, but the application cannot be built
		assert.Equal(t, 2, tr1d1um([]string{applicationName, "-f", path}))
	})
}

// TestTr1d1um_Run starts tr1d1um as main does and stops it with SIGTERM, as a
// deployment would.
func TestTr1d1um_Run(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stopping the application needs SIGTERM")
	}

	logFile := filepath.Join(t.TempDir(), "tr1d1um.log")
	path := writeAppConfig(t, newStore(t).URL, logFile, nil)

	exitCode := make(chan int, 1)
	go func() {
		exitCode <- tr1d1um([]string{applicationName, "-f", path})
	}()

	// fx logs "started" once every server is listening and its signal
	// handling is in place.  Signaling before then would kill the test.
	require.Eventually(t, func() bool {
		written, err := os.ReadFile(filepath.Clean(logFile))
		return err == nil && strings.Contains(string(written), `"msg":"started"`)
	}, 10*time.Second, 10*time.Millisecond, "tr1d1um did not start")

	self, err := os.FindProcess(os.Getpid())
	require.NoError(t, err)
	require.NoError(t, self.Signal(syscall.SIGTERM))

	select {
	case code := <-exitCode:
		assert.Equal(t, 0, code)
	case <-time.After(10 * time.Second):
		t.Fatal("tr1d1um did not stop")
	}
}

// configValues are the configured values the application is given.
type configValues struct {
	fx.In
	RequestMaxRetries    int           `name:"requestMaxRetries"`
	RequestRetryInterval time.Duration `name:"requestRetryInterval"`
	TargetURL            string        `name:"targetURL"`
	WRPSource            string        `name:"WRPSource"`
	SupportedServices    []string      `name:"supportedServices"`
}

// TestDefaults checks the values the application is given when the
// configuration leaves them out.
func TestDefaults(t *testing.T) {
	path := writeConfig(t, t.TempDir(), "c.yaml", "logging:\n  level: info\n")
	v, _, err := setup(flags(t, "-f", path))
	require.NoError(t, err)

	var got configValues
	app := fxtest.New(t,
		fx.Supply(v),
		provideConfig(),
		fx.Invoke(func(c configValues) { got = c }),
	)
	app.RequireStart().RequireStop()

	assert.Equal(t, 2, got.RequestMaxRetries)
	assert.Equal(t, 2*time.Second, got.RequestRetryInterval)
	assert.Equal(t, "localhost:6000", got.TargetURL)
	assert.Equal(t, "dns:localhost", got.WRPSource)
	assert.Empty(t, got.SupportedServices)
}
