// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/touchstone"
	"github.com/xmidt-org/touchstone/touchhttp"
	webhook "github.com/xmidt-org/webhook-schema"
)

const (
	testMaxTTL = 5 * time.Minute
	testBase   = `"registered_from_address": "http://localhost",
		"config": {"url": "http://localhost/hook", "content_type": "json"},
		"events": ["device-status"]`
)

var testNow = time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)

// fixV2Result is what a request through the middleware produced.
type fixV2Result struct {
	rec     *httptest.ResponseRecorder
	reached string // which handler ran: "", "next" or "v2"
	got     webhook.RegistrationV1
	raw     string
}

// serveFixV2 runs a request through the fixV2Duration middleware.  A non-nil
// v2 handler is passed to the middleware; otherwise requests go to next.
func serveFixV2(t *testing.T, version string, body io.Reader, withV2Handler bool) fixV2Result {
	t.Helper()

	var res fixV2Result
	handler := func(name string) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res.reached = name
			b, err := io.ReadAll(r.Body)
			assert.NoError(t, err)
			res.raw = string(b)
			_ = json.Unmarshal(b, &res.got)
			w.WriteHeader(http.StatusOK)
		})
	}

	var v2 http.Handler
	if withV2Handler {
		v2 = handler("v2")
	}

	cfg := schema.TTLVConfig{Max: testMaxTTL, Now: func() time.Time { return testNow }}
	req := httptest.NewRequest(http.MethodPost, "/hook", body)
	req = mux.SetURLVars(req, map[string]string{"version": version})
	res.rec = httptest.NewRecorder()
	fixV2Duration(cfg, v2)(handler("next")).ServeHTTP(res.rec, req)

	return res
}

func registration(extra string) io.Reader {
	return strings.NewReader("{" + testBase + extra + "}")
}

func TestFixV2Duration_NotV2(t *testing.T) {
	res := serveFixV2(t, "v3", strings.NewReader("not even json"), true)

	assert.Equal(t, "next", res.reached, "non-v2 requests skip the v2 handler")
	assert.Equal(t, http.StatusOK, res.rec.Code)
	assert.Equal(t, "not even json", res.raw)
}

func TestFixV2Duration_NoURLVars(t *testing.T) {
	cfg := schema.TTLVConfig{Max: testMaxTTL}
	var reached bool
	next := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true })

	// no mux.SetURLVars, and a zero Now exercises the time.Now default
	fixV2Duration(cfg, nil)(next).ServeHTTP(httptest.NewRecorder(),
		httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader("{")))

	assert.True(t, reached)
}

func TestFixV2Duration_BodyReadError(t *testing.T) {
	res := serveFixV2(t, prevAPIVersion, iotest.ErrReader(io.ErrUnexpectedEOF), false)

	assert.Empty(t, res.reached)
	assert.Equal(t, http.StatusInternalServerError, res.rec.Code)
	assert.Contains(t, res.rec.Body.String(), io.ErrUnexpectedEOF.Error())
}

func TestFixV2Duration_BadJSON(t *testing.T) {
	res := serveFixV2(t, prevAPIVersion, strings.NewReader("{"), false)

	assert.Empty(t, res.reached)
	assert.Equal(t, http.StatusBadRequest, res.rec.Code)
	assert.Contains(t, res.rec.Body.String(), errFailedWebhookUnmarshal.Error())
}

func TestFixV2Duration_WrongFieldType(t *testing.T) {
	res := serveFixV2(t, prevAPIVersion,
		strings.NewReader(`{"registered_from_address": "http://localhost", "events": 5}`), false)

	assert.Empty(t, res.reached)
	assert.Equal(t, http.StatusBadRequest, res.rec.Code)
	assert.Contains(t, res.rec.Body.String(), "events must be of type")
}

func TestFixV2Duration(t *testing.T) {
	inRange := testNow.Add(time.Minute).Format(time.RFC3339)
	tooFar := testNow.Add(time.Hour).Format(time.RFC3339)

	tests := []struct {
		description  string
		extra        string
		wantDuration time.Duration
		wantUntil    time.Time
		wantWarning  string
	}{
		{
			description:  "valid duration is untouched",
			extra:        `, "duration": "1m"`,
			wantDuration: time.Minute,
		}, {
			description:  "unset duration and until default to the max",
			wantDuration: testMaxTTL,
			wantWarning:  "Unset duration and until",
		}, {
			description:  "duration over the max is clamped",
			extra:        `, "duration": "10m"`,
			wantDuration: testMaxTTL,
			wantWarning:  "Invalid duration",
		}, {
			description: "until within the max is untouched",
			extra:       `, "until": "` + inRange + `"`,
			wantUntil:   testNow.Add(time.Minute),
		}, {
			description:  "until beyond the max is cleared and the duration clamped",
			extra:        `, "until": "` + tooFar + `"`,
			wantDuration: testMaxTTL,
			wantWarning:  "Invalid until",
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			res := serveFixV2(t, prevAPIVersion, registration(tc.extra), false)

			require.Equal(t, "next", res.reached)
			assert.Equal(t, http.StatusOK, res.rec.Code)
			assert.Equal(t, tc.wantDuration, time.Duration(res.got.Duration))
			assert.True(t, tc.wantUntil.Equal(res.got.Until), "until: want %v, got %v", tc.wantUntil, res.got.Until)
			assert.Equal(t, "http://localhost", res.got.Address)
			assert.Equal(t, []string{"device-status"}, res.got.Events)

			warning := res.rec.Header().Get(v2WarningHeader)
			if tc.wantWarning == "" {
				assert.Empty(t, warning)
			} else {
				assert.Contains(t, warning, tc.wantWarning)
			}
		})
	}
}

func TestFixV2Duration_UsesV2Handler(t *testing.T) {
	res := serveFixV2(t, prevAPIVersion, registration(`, "duration": "1m"`), true)

	assert.Equal(t, "v2", res.reached, "a configured v2 handler replaces next")
	assert.Equal(t, http.StatusOK, res.rec.Code)
	assert.Equal(t, time.Minute, time.Duration(res.got.Duration))
}

// echoHandler responds with its name so tests can tell which handler ran.
func echoHandler(name string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, name)
	})
}

// requireAuth is a stand-in auth chain that rejects requests without a
// credential, so a test can tell whether a route went through the chain.
func requireAuth() alice.Chain {
	return alice.New(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") == "" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	})
}

func do(h http.Handler, method, path string, authed bool, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if authed {
		req.Header.Set("Authorization", "Basic dGVzdA==")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	return rec
}

func newWebhookRouter() (*mux.Router, *mux.Router) {
	root := mux.NewRouter()
	return root, root.PathPrefix("/api/{version}").Subrouter()
}

func TestHandleWebhookRoutes_NoHandlers(t *testing.T) {
	root, api := newWebhookRouter()

	err := handleWebhookRoutes(handleWebhookRoutesIn{APIRouter: api, AuthChain: requireAuth()})

	require.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, do(root, http.MethodPost, "/api/v3/hook", true, "").Code)
	assert.Equal(t, http.StatusNotFound, do(root, http.MethodGet, "/api/v3/hooks", true, "").Code)
}

func TestHandleWebhookRoutes(t *testing.T) {
	root, api := newWebhookRouter()
	err := handleWebhookRoutes(handleWebhookRoutesIn{
		APIRouter:             api,
		AuthChain:             requireAuth(),
		AddWebhookHandler:     echoHandler("add"),
		V2AddWebhookHandler:   echoHandler("add-v2"),
		GetAllWebhooksHandler: echoHandler("get-all"),
		WebhookConfig:         ancla.Config{Validation: schema.SchemaURLValidatorConfig{TTL: schema.TTLVConfig{Max: testMaxTTL}}},
	})
	require.NoError(t, err)

	body := "{" + testBase + `, "duration": "1m"}`
	tests := []struct {
		description string
		method      string
		path        string
		authed      bool
		wantCode    int
		wantBody    string
	}{
		{"add", http.MethodPost, "/api/v3/hook", true, http.StatusOK, "add"},
		{"add needs auth", http.MethodPost, "/api/v3/hook", false, http.StatusUnauthorized, ""},
		// gorilla/mux reports 404 rather than 405 here because "/hook" is followed by "/hooks"
		{"add is POST only", http.MethodGet, "/api/v3/hook", true, http.StatusNotFound, "404 page not found\n"},
		{"add on the previous version", http.MethodPost, "/api/" + prevAPIVersion + "/hook", true, http.StatusOK, "add-v2"},
		{"get all", http.MethodGet, "/api/v3/hooks", true, http.StatusOK, "get-all"},
		{"get all needs auth", http.MethodGet, "/api/v3/hooks", false, http.StatusUnauthorized, ""},
		{"get all is GET only", http.MethodPost, "/api/v3/hooks", true, http.StatusMethodNotAllowed, ""},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			rec := do(root, tc.method, tc.path, tc.authed, body)

			assert.Equal(t, tc.wantCode, rec.Code)
			assert.Equal(t, tc.wantBody, rec.Body.String())
		})
	}
}

func TestHandleWebhookRoutes_OnlyOneHandler(t *testing.T) {
	root, api := newWebhookRouter()

	err := handleWebhookRoutes(handleWebhookRoutesIn{
		APIRouter:             api,
		AuthChain:             requireAuth(),
		GetAllWebhooksHandler: echoHandler("get-all"),
	})

	require.NoError(t, err)
	rec := do(root, http.MethodGet, "/api/v3/hooks", true, "")
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "get-all", rec.Body.String())
}

// failingRegisterer rejects every collector, as a registry would on a
// conflicting metric definition.
type failingRegisterer struct{ prometheus.Registerer }

func (failingRegisterer) Register(prometheus.Collector) error { return errors.New("cannot register") }
func (failingRegisterer) MustRegister(...prometheus.Collector) {
	panic("cannot register")
}
func (failingRegisterer) Unregister(prometheus.Collector) bool { return false }

func TestMetricMiddleware(t *testing.T) {
	reg := prometheus.NewPedanticRegistry()
	out := metricMiddleware(touchstone.NewFactory(touchstone.Config{}, zap.NewNop(), reg))

	for name, chain := range map[string]alice.Chain{
		"server_primary":   out.Primary,
		"server_alternate": out.Alternate,
		"server_health":    out.Health,
	} {
		t.Run(name, func(t *testing.T) {
			rec := do(chain.Then(echoHandler("ok")), http.MethodGet, "/", false, "")

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "ok", rec.Body.String())
		})
	}

	// each chain records into its own server label
	families, err := reg.Gather()
	require.NoError(t, err)
	servers := map[string]bool{}
	for _, mf := range families {
		for _, m := range mf.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == touchhttp.ServerLabel {
					servers[l.GetValue()] = true
				}
			}
		}
	}
	assert.Equal(t, map[string]bool{"server_primary": true, "server_alternate": true, "server_health": true}, servers)
}

// When the metrics cannot be registered the middleware yields empty chains,
// which pass requests straight through.
func TestMetricMiddleware_RegistrationFailure(t *testing.T) {
	out := metricMiddleware(touchstone.NewFactory(touchstone.Config{}, zap.NewNop(), failingRegisterer{}))

	for name, chain := range map[string]alice.Chain{"primary": out.Primary, "alternate": out.Alternate, "health": out.Health} {
		t.Run(name, func(t *testing.T) {
			rec := do(chain.Then(echoHandler("ok")), http.MethodGet, "/", false, "")

			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "ok", rec.Body.String())
		})
	}
}

func viperFromYAML(t *testing.T, yaml string) *viper.Viper {
	t.Helper()

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(yaml)))

	return v
}

func TestProvideParamFilters(t *testing.T) {
	tests := []struct {
		description string
		yaml        string
		getLogged   map[string]bool
		patchLogged map[string]bool
	}{
		{
			description: "absent config logs no values",
			yaml:        "other: 1",
			getLogged:   map[string]bool{"Device.WiFi.SSID": false},
			patchLogged: map[string]bool{"Device.WiFi.SSID": false},
		}, {
			description: "get and patch are configured independently",
			yaml: `
parameterLogging:
  get:
    mode: never
    except:
      - "Device.WiFi.**"
  patch:
    mode: always
    except:
      - "Device.DeviceInfo.SerialNumber"
`,
			getLogged: map[string]bool{
				"Device.WiFi.Radio.1.SSID":       true,
				"Device.DeviceInfo.SerialNumber": false,
				"Device.DeviceInfo.Manufacturer": false,
			},
			patchLogged: map[string]bool{
				"Device.WiFi.Radio.1.SSID":       true,
				"Device.DeviceInfo.SerialNumber": false,
				"Device.DeviceInfo.Manufacturer": true,
			},
		}, {
			description: "only one operation configured",
			yaml: `
parameterLogging:
  patch:
    mode: always
`,
			getLogged:   map[string]bool{"Device.Any.Name": false},
			patchLogged: map[string]bool{"Device.Any.Name": true},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			f, err := provideParamFilters(viperFromYAML(t, tc.yaml))

			require.NoError(t, err)
			require.NotNil(t, f)
			for name, want := range tc.getLogged {
				assert.Equal(t, want, f.GetFilter().ShouldLog(name), "get %s", name)
			}
			for name, want := range tc.patchLogged {
				assert.Equal(t, want, f.PatchFilter().ShouldLog(name), "patch %s", name)
			}
		})
	}
}

func TestProvideParamFilters_BadConfig(t *testing.T) {
	f, err := provideParamFilters(viperFromYAML(t, "parameterLogging: not-a-section"))

	require.Error(t, err)
	assert.Contains(t, err.Error(), "parameterLogging")
	assert.Nil(t, f)
}
