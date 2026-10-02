// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/bascule"
	"go.uber.org/zap"
)

// capPrefix is the configured capability prefix: everything before the
// endpoint, e.g. "example-prefix:api:".  A token's capability is capPrefix
// followed by "{endpoint}:{method}", with the endpoint written relative to the
// API base -- "device/.*/config", not "/api/v3/device/.*/config".
const capPrefix = "example-prefix:api:"

func tokenWith(capabilities ...string) *JWTToken {
	return &JWTToken{principal: "partner-acme", capabilities: capabilities}
}

func request(method, path string) *http.Request {
	return httptest.NewRequest(method, path, nil)
}

// legacyConfig is the shape an existing deployment already has on disk.
func legacyConfig(mode string) CapabilityConfig {
	return CapabilityConfig{
		Type:            mode,
		Prefix:          capPrefix,
		AcceptAllMethod: "all",
	}
}

func approver(t *testing.T, cfg CapabilityConfig) *CapabilityApprover {
	t.Helper()
	a, err := NewCapabilityApprover(cfg, zap.NewNop(), nopCapabilityMetric{})
	require.NoError(t, err)
	return a
}

func TestCapabilityApprover_API(t *testing.T) {
	cases := []struct {
		name         string
		capabilities []string
		defaults     []string
		mode         string
		method       string
		path         string
		allowed      bool
	}{
		{
			name:         "endpoint and method match",
			capabilities: []string{capPrefix + "device/.*/config:get"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      true,
		},
		{
			name:         "previous api version is stripped too",
			capabilities: []string{capPrefix + "device/.*/config:get"},
			method:       http.MethodGet,
			path:         "/api/v2/device/mac:112233445566/config",
			allowed:      true,
		},
		{
			name:         "method does not match",
			capabilities: []string{capPrefix + "device/.*/config:get"},
			method:       http.MethodPatch,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name:         "accept-all method grants every verb",
			capabilities: []string{capPrefix + "device/.*/config:all"},
			method:       http.MethodPatch,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      true,
		},
		{
			name:         "endpoint does not match",
			capabilities: []string{capPrefix + "hook:post"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name:         "pattern must match from the start of the path",
			capabilities: []string{capPrefix + "config:get"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name: "one of several capabilities matches",
			capabilities: []string{
				capPrefix + "hook:post",
				capPrefix + "device/.*/config:get",
			},
			method:  http.MethodGet,
			path:    "/api/v3/device/mac:112233445566/config",
			allowed: true,
		},
		{
			name:         "no capabilities and no default denies",
			capabilities: nil,
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name:         "configured default applies when token carries none",
			capabilities: nil,
			defaults:     []string{"device/.*/config:get"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      true,
		},
		{
			name:         "token capabilities take precedence over the default",
			capabilities: []string{capPrefix + "hook:post"},
			defaults:     []string{"device/.*/config:get"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name:         "monitor allows a request it would otherwise deny",
			capabilities: []string{capPrefix + "hook:post"},
			mode:         "monitor",
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      true,
		},
		{
			name:         "capability without the configured prefix is ignored",
			capabilities: []string{"other-prefix:api:device/.*/config:get"},
			method:       http.MethodGet,
			path:         "/api/v3/device/mac:112233445566/config",
			allowed:      false,
		},
		{
			name: "capabilities with a different prefix are ignored",
			capabilities: []string{
				"example-prefix:tr181:Device.**", // future work, different prefix
				capPrefix + "device/.*/config:get",
			},
			method:  http.MethodGet,
			path:    "/api/v3/device/mac:112233445566/config",
			allowed: true,
		},
		{
			name:         "malformed api capability grants nothing",
			capabilities: []string{capPrefix + "no-method-here"},
			method:       http.MethodGet,
			path:         "/api/v3/no-method-here",
			allowed:      false,
		},
		{
			name:         "unversioned path is matched as-is",
			capabilities: []string{capPrefix + "health:get"},
			method:       http.MethodGet,
			path:         "/health",
			allowed:      true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mode := tc.mode
			if mode == "" {
				mode = "enforce"
			}

			cfg := legacyConfig(mode)
			cfg.Default = tc.defaults

			err := approver(t, cfg).Approve(context.Background(),
				request(tc.method, tc.path), tokenWith(tc.capabilities...))

			if tc.allowed {
				assert.NoError(t, err)
				return
			}
			assert.Error(t, err)
		})
	}
}

// principalOnlyToken carries no capabilities, as a basic-auth token does.
type principalOnlyToken struct{ principal string }

func (t principalOnlyToken) Principal() string { return t.principal }

// TestCapabilityApprover_Mode covers the compatibility rule that matters most:
// an existing config that never set a usable type must not start rejecting
// traffic.

func TestCapabilityApprover_Mode(t *testing.T) {
	tests := []struct {
		description string
		mode        string
		token       bascule.Token
		expectedErr bool
	}{
		{
			description: "omitted type leaves the feature off",
			mode:        "",
			token:       tokenWith(),
		}, {
			description: "disabled leaves the feature off",
			mode:        "disabled",
			token:       tokenWith(),
		}, {
			description: "enforce rejects a token nothing grants",
			mode:        "enforce",
			token:       tokenWith(),
			expectedErr: true,
		}, {
			description: "monitor records but allows",
			mode:        "monitor",
			token:       tokenWith(),
		}, {
			description: "a token that cannot carry capabilities grants nothing",
			mode:        "enforce",
			token:       principalOnlyToken{principal: "nobody"},
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			err := approver(t, legacyConfig(tc.mode)).Approve(context.Background(),
				request(http.MethodGet, "/api/v3/device/mac:112233445566/config"), tc.token)

			if tc.expectedErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
		})
	}
}

// A mode that is neither recognized nor empty must stop the server rather than
// quietly disable the check it was meant to configure.
func TestParseMode(t *testing.T) {
	tests := []struct {
		description     string
		mode            string
		expected        CapabilityMode
		expectedEnabled bool
		expectedErr     bool
	}{
		{description: "enforce", mode: "enforce", expected: CapabilityEnforce, expectedEnabled: true},
		{description: "monitor", mode: "monitor", expected: CapabilityMonitor, expectedEnabled: true},
		{description: "disabled is off", mode: "disabled", expected: CapabilityDisabled},
		{description: "empty is off", mode: ""},
		{description: "whitespace is off", mode: "   "},
		{description: "padded value trimmed", mode: " enforce ", expected: CapabilityEnforce, expectedEnabled: true},
		{description: "wrong case rejected", mode: "ENFORCE", expectedErr: true},
		{description: "misspelling rejected", mode: "Enforced", expectedErr: true},
		{description: "unrelated value rejected", mode: "yes", expectedErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			mode, enabled, err := parseMode(tc.mode)

			if tc.expectedErr {
				assert.Error(t, err)
				assert.False(t, enabled)
				return
			}

			assert.NoError(t, err)
			assert.Equal(t, tc.expected, mode)
			assert.Equal(t, tc.expectedEnabled, enabled)
		})
	}
}

func TestNewCapabilityApprover_Config(t *testing.T) {
	tests := []struct {
		description string
		config      CapabilityConfig
		expectedErr bool
	}{
		{
			description: "enforce with a prefix",
			config:      legacyConfig("enforce"),
		}, {
			description: "omitted type leaves checking off",
			config:      CapabilityConfig{},
		}, {
			description: "disabled type leaves checking off",
			config:      CapabilityConfig{Type: "disabled"},
		}, {
			description: "unrecognized mode is rejected",
			config:      CapabilityConfig{Type: "Enforced", Prefix: capPrefix},
			expectedErr: true,
		}, {
			description: "enforce with no prefix is rejected",
			config:      CapabilityConfig{Type: "enforce"},
			expectedErr: true,
		}, {
			description: "default is applied",
			config: CapabilityConfig{
				Type: "enforce", Prefix: capPrefix,
				Default: []string{"device/.*/config:get"},
			},
		}, {
			description: "default that will not compile is rejected",
			config: CapabilityConfig{
				Type: "enforce", Prefix: capPrefix,
				Default: []string{"[unclosed:get"},
			},
			expectedErr: true,
		}, {
			description: "default missing a method is rejected",
			config: CapabilityConfig{
				Type: "enforce", Prefix: capPrefix,
				Default: []string{"no-method"},
			},
			expectedErr: true,
		}, {
			description: "a bad default is not compiled when checking is off",
			config: CapabilityConfig{
				Type: "disabled", Prefix: capPrefix,
				Default: []string{"[unclosed:get"},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			got, err := NewCapabilityApprover(tc.config, zap.NewNop(), nopCapabilityMetric{})

			if tc.expectedErr {
				assert.Error(t, err)
				assert.Nil(t, got)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, got)
		})
	}
}

// TestParseSpec pins the endpoint/method split, which keys off the final colon
// and then validates the method.
func TestParseSpec(t *testing.T) {
	checker := apiChecker{acceptAllMethod: "all"}

	tests := []struct {
		description      string
		tail             string
		expectedEndpoint string
		expectedMethod   string
		expectedErr      bool
	}{
		{
			description:      "endpoint and verb",
			tail:             "device/.*/config:get",
			expectedEndpoint: "device/.*/config",
			expectedMethod:   "get",
		}, {
			description:      "accept-all method",
			tail:             "device/.*/config:all",
			expectedEndpoint: "device/.*/config",
			expectedMethod:   "all",
		}, {
			description:      "colon in the endpoint stays with the endpoint",
			tail:             "device/mac:.*/stat:get",
			expectedEndpoint: "device/mac:.*/stat",
			expectedMethod:   "get",
		}, {
			description:      "a path segment named like a verb is not the method",
			tail:             "device/delete:post",
			expectedEndpoint: "device/delete",
			expectedMethod:   "post",
		}, {
			description: "no method",
			tail:        "device/foo",
			expectedErr: true,
		}, {
			description: "unknown method",
			tail:        "device/foo:frobnicate",
			expectedErr: true,
		}, {
			description: "empty endpoint",
			tail:        ":get",
			expectedErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			endpoint, method, err := checker.parseSpec(tc.tail)

			if tc.expectedErr {
				assert.Error(t, err)
				return
			}
			assert.NoError(t, err)
			assert.Equal(t, tc.expectedEndpoint, endpoint)
			assert.Equal(t, tc.expectedMethod, method)
		})
	}
}

// A capability whose accept-all value is a custom token is honored.
func TestParseSpec_CustomAcceptAll(t *testing.T) {
	checker := apiChecker{acceptAllMethod: "any"}

	_, method, err := checker.parseSpec("device/.*/config:any")
	assert.NoError(t, err)
	assert.Equal(t, "any", method)

	_, _, err = checker.parseSpec("device/.*/config:all")
	assert.Error(t, err, "with a custom accept-all, \"all\" is no longer a known method")
}

func TestStripAPIBase(t *testing.T) {
	tests := []struct {
		description string
		path        string
		expected    string
	}{
		{description: "current version stripped", path: "/api/v3/device/mac:1/config", expected: "/device/mac:1/config"},
		{description: "previous version stripped", path: "/api/v2/device/mac:1/config", expected: "/device/mac:1/config"},
		{description: "unknown version left alone", path: "/api/v9/device/mac:1/config", expected: "/api/v9/device/mac:1/config"},
		{description: "unversioned path left alone", path: "/health", expected: "/health"},
		{description: "empty path", path: "", expected: ""},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert.Equal(t, tc.expected, stripAPIBase(tc.path))
		})
	}
}

func TestJWTToken_Capabilities(t *testing.T) {
	tests := []struct {
		description string
		token       *JWTToken
		expected    []string
	}{
		{
			description: "no capabilities",
			token:       &JWTToken{principal: "alice"},
		}, {
			description: "one capability",
			token:       tokenWith(capPrefix + "foo:get"),
			expected:    []string{capPrefix + "foo:get"},
		}, {
			description: "several capabilities",
			token:       tokenWith(capPrefix+"foo:get", capPrefix+"bar:post"),
			expected:    []string{capPrefix + "foo:get", capPrefix + "bar:post"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.description, func(t *testing.T) {
			assert.Equal(t, tc.expected, tc.token.Capabilities())
		})
	}
}
