// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/ancla"
	anclaschema "github.com/xmidt-org/ancla/schema"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/sallust"
	"github.com/xmidt-org/touchstone/touchhttp"
	webhook "github.com/xmidt-org/webhook-schema"
	"go.uber.org/zap"
)

func TestProvideURLPrefix(t *testing.T) {
	tcs := []struct {
		name           string
		prevVerSupport bool
		expected       string
	}{
		{
			name:           "uses dual-version prefix when previous-version support is enabled",
			prevVerSupport: true,
			expected:       "/" + apiBaseDualVersion,
		},
		{
			name:           "uses v3-only prefix when previous-version support is disabled",
			prevVerSupport: false,
			expected:       "/" + apiBase,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			actual := provideURLPrefix(provideURLPrefixIn{PrevVerSupport: tc.prevVerSupport})
			assert.Equal(t, tc.expected, actual)
		})
	}
}

func TestProvideAPIRouter(t *testing.T) {
	tcs := []struct {
		name       string
		prefix     string
		requestURI string
		expectCode int
	}{
		{
			name:       "matches fixed v3 prefix",
			prefix:     "/api/v3",
			requestURI: "/api/v3/ping",
			expectCode: http.StatusNoContent,
		},
		{
			name:       "matches dual-version prefix using v2",
			prefix:     "/api/{version:v3|v2}",
			requestURI: "/api/v2/ping",
			expectCode: http.StatusNoContent,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			primary := mux.NewRouter()
			api := provideAPIRouter(apiRouterIn{PrimaryRouter: primary, URLPrefix: tc.prefix})

			api.HandleFunc("/ping", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusNoContent)
			}).Methods(http.MethodGet)

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.requestURI, nil)
			primary.ServeHTTP(rr, req)
			assert.Equal(t, tc.expectCode, rr.Code)
		})
	}
}

func TestBuildAPIAltRouter(t *testing.T) {
	tcs := []struct {
		name       string
		path       string
		expectCode int
	}{
		{name: "device service route", path: "/api/v3/device/mac123/reboot", expectCode: http.StatusAccepted},
		{name: "device service parameter route", path: "/api/v3/device/mac123/get/value", expectCode: http.StatusAccepted},
		{name: "stat route", path: "/api/v3/device/mac123/stat", expectCode: http.StatusAccepted},
		{name: "hook route", path: "/api/v3/hook", expectCode: http.StatusAccepted},
		{name: "hooks route", path: "/api/v3/hooks", expectCode: http.StatusAccepted},
		{name: "unmatched route", path: "/api/v3/not-found", expectCode: http.StatusNotFound},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			primary := mux.NewRouter()
			api := provideAPIRouter(apiRouterIn{PrimaryRouter: primary, URLPrefix: "/api/v3"})
			api.HandleFunc("/device/{deviceid}/{service}", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			api.HandleFunc("/device/{deviceid}/{service}/{parameter}", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			api.HandleFunc("/device/{deviceid}/stat", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			api.HandleFunc("/hook", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})
			api.HandleFunc("/hooks", func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusAccepted)
			})

			alternate := mux.NewRouter()
			buildAPIAltRouter(apiAltRouterIn{APIRouter: api, AlternateRouter: alternate, URLPrefix: "/api/v3"})

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			alternate.ServeHTTP(rr, req)
			assert.Equal(t, tc.expectCode, rr.Code)
		})
	}
}

func TestBuildMetricsRoutes(t *testing.T) {
	h := touchhttp.Handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	tcs := []struct {
		name       string
		router     *mux.Router
		handler    touchhttp.Handler
		doRequest  bool
		expectCode int
	}{
		{name: "nil router is a no-op", router: nil, handler: h, doRequest: false},
		{name: "nil handler is a no-op", router: mux.NewRouter(), handler: nil, doRequest: true, expectCode: http.StatusNotFound},
		{name: "registers metrics endpoint", router: mux.NewRouter(), handler: h, doRequest: true, expectCode: http.StatusNoContent},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			require.NotPanics(t, func() {
				buildMetricsRoutes(metricsRoutesIn{Router: tc.router, Handler: tc.handler})
			})

			if !tc.doRequest || tc.router == nil {
				return
			}

			rr := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			tc.router.ServeHTTP(rr, req)
			assert.Equal(t, tc.expectCode, rr.Code)
		})
	}
}

func TestHandleWebhookRoutes(t *testing.T) {
	addHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusCreated)
	})
	getAllHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	tcs := []struct {
		name           string
		add            http.Handler
		v2             http.Handler
		getAll         http.Handler
		ttl            anclaschema.TTLVConfig
		doPostHook     bool
		doGetHooks     bool
		expectErr      bool
		expectPostCode int
		expectGetCode  int
	}{
		{
			name:           "no handlers configured does nothing",
			add:            nil,
			v2:             nil,
			getAll:         nil,
			doPostHook:     true,
			doGetHooks:     true,
			expectPostCode: http.StatusNotFound,
			expectGetCode:  http.StatusNotFound,
		},
		{
			name:           "registers webhook routes when handlers are present",
			add:            addHandler,
			v2:             nil,
			getAll:         getAllHandler,
			ttl:            anclaschema.TTLVConfig{Max: 5 * time.Minute, Now: time.Now},
			doPostHook:     true,
			doGetHooks:     true,
			expectPostCode: http.StatusCreated,
			expectGetCode:  http.StatusOK,
		},
		{
			name:      "returns error when ttl config is invalid",
			add:       addHandler,
			v2:        nil,
			getAll:    getAllHandler,
			ttl:       anclaschema.TTLVConfig{Max: -1 * time.Second},
			expectErr: true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			api := mux.NewRouter()
			err := handleWebhookRoutes(handleWebhookRoutesIn{
				Logger:                zap.NewNop(),
				Tracing:               candlelight.Tracing{},
				APIRouter:             api,
				AuthChain:             alice.New(),
				AddWebhookHandler:     tc.add,
				V2AddWebhookHandler:   tc.v2,
				GetAllWebhooksHandler: tc.getAll,
				WebhookConfig:         ancla.Config{Validation: anclaschema.SchemaURLValidatorConfig{TTL: tc.ttl}},
			})

			if tc.expectErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)

			if tc.doPostHook {
				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodPost, "/hook", strings.NewReader(`{"events":["event"],"config":{"url":"https://example.com"}}`))
				ctx := sallust.With(req.Context(), zap.NewNop())
				api.ServeHTTP(rr, req.WithContext(ctx))
				assert.Equal(t, tc.expectPostCode, rr.Code)
			}

			if tc.doGetHooks {
				rr := httptest.NewRecorder()
				req := httptest.NewRequest(http.MethodGet, "/hooks", nil)
				api.ServeHTTP(rr, req)
				assert.Equal(t, tc.expectGetCode, rr.Code)
			}
		})
	}
}

func TestFixV2Duration(t *testing.T) {
	fixedNow := func() time.Time {
		return time.Date(2026, time.May, 14, 10, 0, 0, 0, time.UTC)
	}

	tcs := []struct {
		name                string
		cfg                 anclaschema.TTLVConfig
		vars                map[string]string
		body                string
		expectCtorErr       bool
		expectCode          int
		expectWarningHeader bool
		expectDuration      webhook.CustomDuration
		expectUntilZero     bool
	}{
		{
			name:          "fails constructor on negative max",
			cfg:           anclaschema.TTLVConfig{Max: -1 * time.Second, Now: fixedNow},
			expectCtorErr: true,
		},
		{
			name:          "fails constructor on negative jitter",
			cfg:           anclaschema.TTLVConfig{Max: 5 * time.Minute, Jitter: -1 * time.Second, Now: fixedNow},
			expectCtorErr: true,
		},
		{
			name:            "non-v2 request passes through unchanged",
			cfg:             anclaschema.TTLVConfig{Max: 5 * time.Minute, Now: fixedNow},
			vars:            map[string]string{"version": apiVersion},
			body:            `{}`,
			expectCode:      http.StatusNoContent,
			expectDuration:  0,
			expectUntilZero: true,
		},
		{
			name:       "bad json type in v2 returns bad request",
			cfg:        anclaschema.TTLVConfig{Max: 5 * time.Minute, Now: fixedNow},
			vars:       map[string]string{"version": prevAPIVersion},
			body:       `{"duration":{}}`,
			expectCode: http.StatusBadRequest,
		},
		{
			name:                "v2 missing duration and until defaults duration",
			cfg:                 anclaschema.TTLVConfig{Max: 5 * time.Minute, Jitter: time.Second, Now: fixedNow},
			vars:                map[string]string{"version": prevAPIVersion},
			body:                `{"events":["event"],"config":{"url":"https://example.com"}}`,
			expectCode:          http.StatusNoContent,
			expectWarningHeader: true,
			expectDuration:      webhook.CustomDuration(5 * time.Minute),
			expectUntilZero:     true,
		},
		{
			name:                "v2 invalid until defaults duration and clears until",
			cfg:                 anclaschema.TTLVConfig{Max: 5 * time.Minute, Jitter: time.Second, Now: fixedNow},
			vars:                map[string]string{"version": prevAPIVersion},
			body:                `{"events":["event"],"config":{"url":"https://example.com"},"until":"2026-05-14T10:20:00Z"}`,
			expectCode:          http.StatusNoContent,
			expectWarningHeader: true,
			expectDuration:      webhook.CustomDuration(5 * time.Minute),
			expectUntilZero:     true,
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			ctor, err := fixV2Duration(func(context.Context) *zap.Logger { return zap.NewNop() }, tc.cfg, nil)
			if tc.expectCtorErr {
				require.Error(t, err)
				assert.Nil(t, ctor)
				return
			}

			require.NoError(t, err)

			var observed webhook.RegistrationV1
			h := ctor(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				defer r.Body.Close()
				b, readErr := io.ReadAll(r.Body)
				require.NoError(t, readErr)
				if len(b) > 0 {
					_ = json.Unmarshal(b, &observed)
				}
				w.WriteHeader(http.StatusNoContent)
			}))

			req := httptest.NewRequest(http.MethodPost, "/hook", bytes.NewBufferString(tc.body))
			req = mux.SetURLVars(req, tc.vars)
			rr := httptest.NewRecorder()
			h.ServeHTTP(rr, req)

			assert.Equal(t, tc.expectCode, rr.Code)
			if tc.expectCode != http.StatusNoContent {
				assert.Contains(t, rr.Body.String(), errFailedWebhookUnmarshal.Error())
				return
			}

			if tc.expectWarningHeader {
				assert.NotEmpty(t, rr.Header().Values(v2WarningHeader))
			} else {
				assert.Empty(t, rr.Header().Values(v2WarningHeader))
			}

			assert.Equal(t, tc.expectDuration, observed.Duration)
			assert.Equal(t, tc.expectUntilZero, observed.Until.IsZero())
		})
	}
}
