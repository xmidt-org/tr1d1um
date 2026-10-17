// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jws"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

const (
	testParamPrefix  = "x1:webpa:param:"
	paramPrefixesKey = paramConfigKey + ".Prefixes"

	// a few parameter names the tests share
	modelName  = "Device.DeviceInfo.ModelName"
	deviceInfo = "Device.DeviceInfo.**"
	anything   = "Device.**"
	notAllowed = "would-reject; kind=param; reason=parameter-not-allowed"
	// malformedAnything is a capability with a typo in its operation.
	malformedAnything = anything + ":wirte"
	rejected          = "rejected; kind=param; reason=parameter-not-allowed"
	malformed         = "malformed; kind=param"
)

// protectParams builds the chain with the parameter check applied to a
// handler, finding every request's parameters with extract.
func protectParams(t *testing.T, cfg inboundConfig, v *viper.Viper, kp jws.KeyProvider, extract ParamExtractor) (http.Handler, *prometheus.CounterVec, *prometheus.CounterVec) {
	t.Helper()

	checks, warnings := testCounter(), testWarningCounter()
	chain, params, err := NewMiddleware(cfg, v, kp, zap.NewNop(), checks, warnings)
	require.NoError(t, err)

	protected := params.Middleware(extract)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Reached", "yes")
		w.WriteHeader(http.StatusOK)
	}))

	return chain.Then(protected), checks, warnings
}

func TestNewMiddleware_Param(t *testing.T) {
	keys := newTestKeys(t)
	future := time.Now().Add(time.Hour)

	// token signs a token for testPrincipal granting the endpoint, with the
	// given parameter capabilities.
	token := func(caps ...string) string {
		return "Bearer " + keys.sign(t, testKeyID, future, map[string]any{
			capabilitiesClaim: append([]string{testPrefix + "device/.*/config:all"}, caps...),
			allowedResources:  partners(testPartner),
		})
	}

	read := func(names ...string) ParamRequest { return ParamRequest{Operation: OperationRead, Names: names} }
	write := func(names ...string) ParamRequest { return ParamRequest{Operation: OperationWrite, Names: names} }
	enforcing := paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModeEnforce}

	cases := []struct {
		name            string
		cfg             paramConfig
		paramConfigured bool // whether the param section is present at all
		silent          bool // the mode keeps wantWarnings from the caller; they are only counted
		caps            []string
		req             ParamRequest
		nothingToCheck  bool // the extractor finds no parameters
		wantStatus      int
		wantWarnings    []string // prefixes, one per expected warning header
		wantRejectedFor string   // the name the rejection header should carry, when checked
	}{
		{
			name:       "param not configured: parameter capabilities are ignored",
			caps:       []string{testParamPrefix + "Device.Nothing:read"},
			req:        read(modelName),
			wantStatus: http.StatusOK,
		}, {
			name:            "no parameter capability is unrestricted",
			paramConfigured: true,
			cfg:             enforcing,
			req:             write(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            "another parameter prefix is ignored",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{"other:param:Device.Nothing:read"},
			req:             read(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            "nothing to check passes through",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.Nothing:read"},
			nothingToCheck:  true,
			wantStatus:      http.StatusOK,
		}, {
			name:            "exact name",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + modelName + ":read"},
			req:             read(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            "matching ignores case",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "device.deviceinfo.modelname:read"},
			req:             read("DEVICE.DeviceInfo.ModelName"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "another name",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + modelName + ":read"},
			req:             read("Device.DeviceInfo.SerialNumber"),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "* matches within one level",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.WiFi.SSID.*.SSID:read"},
			req:             read("Device.WiFi.SSID.1.SSID"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "* does not cross a level",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.WiFi.SSID.*.SSID:read"},
			req:             read("Device.WiFi.SSID.1.2.SSID"),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "** crosses levels",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             read("Device.DeviceInfo.Processor.1.Architecture"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "** between dots needs a level",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.Foo.**.Bar"},
			req:             read("Device.Foo.Bar"),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "** between dots takes several levels",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.Foo.**.Bar"},
			req:             read("Device.Foo.1.2.Bar"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "a wildcard may match part of a name",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.WiFi**"},
			req:             read("Device.WiFiSecrets.Key"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "read-only capability refuses a write",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             write(modelName),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "write-only capability refuses a read",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":write"},
			req:             read(modelName),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "write-only capability allows a write",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":write"},
			req:             write(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            ":all allows both",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":all"},
			req:             write(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            "no suffix means all",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo},
			req:             write(modelName),
			wantStatus:      http.StatusOK,
		}, {
			name:            "every name must be covered, and the first not covered is named",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             read(modelName, "Device.WiFi.Radio.1.Channel", "Device.DeviceInfo.SerialNumber"),
			wantStatus:      http.StatusForbidden,
			wantRejectedFor: "Device.WiFi.Radio.1.Channel",
		}, {
			name:            "names may be covered by different capabilities",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read", testParamPrefix + "Device.WiFi.**:all"},
			req:             read(modelName, "Device.WiFi.Radio.1.Channel"),
			wantStatus:      http.StatusOK,
		}, {
			name:            "partial path covered by its own subtree",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             read("Device.DeviceInfo."),
			wantStatus:      http.StatusOK,
		}, {
			name:            "partial path covered by a wider subtree",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + anything + ":read"},
			req:             read("Device.DeviceInfo."),
			wantStatus:      http.StatusOK,
		}, {
			name:            "partial path covered by a subtree with wildcards before the **",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.WiFi.SSID.*.**:all"},
			req:             write("Device.WiFi.SSID.1."),
			wantStatus:      http.StatusOK,
		}, {
			name:            "partial path not covered by a single level",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.DeviceInfo.*:read"},
			req:             read("Device.DeviceInfo."),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "partial path not covered by a pattern that does not end in **",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.Foo.**.Bar:read"},
			req:             read("Device.Foo."),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "partial path outside the subtree",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             read("Device."),
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "table operation as a partial path write",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + "Device.NAT.PortMapping.**:write"},
			req:             write("Device.NAT.PortMapping.3."),
			wantStatus:      http.StatusOK,
		}, {
			name:            "only a malformed capability is not unrestricted",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + anything + ":wirte"},
			req:             read(modelName),
			wantStatus:      http.StatusForbidden,
			wantWarnings:    []string{`malformed; kind=param; cap="x1:webpa:param:Device.**:wirte"`},
		}, {
			name:            "an empty pattern is malformed",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + ":read"},
			req:             read(modelName),
			wantStatus:      http.StatusForbidden,
			wantWarnings:    []string{malformed},
		}, {
			name:            "a malformed capability beside a matching one warns on success",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + anything + ":wirte", testParamPrefix + anything},
			req:             read(modelName),
			wantStatus:      http.StatusOK,
			wantWarnings:    []string{malformed},
		}, {
			name:            "an unknown operation is allowed by nothing",
			paramConfigured: true,
			cfg:             enforcing,
			caps:            []string{testParamPrefix + anything},
			req:             ParamRequest{Operation: "delete", Names: []string{modelName}},
			wantStatus:      http.StatusForbidden,
		}, {
			name:            "permissive with warnings: a refusal goes through with a warning naming the first parameter",
			paramConfigured: true,
			cfg:             paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModePermissiveWithWarnings},
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             read(modelName, "Device.WiFi.Radio.1.Channel", "Device.WiFi.Radio.2.Channel"),
			wantStatus:      http.StatusOK,
			wantWarnings:    []string{notAllowed + "; op=read; name=Device.WiFi.Radio.1.Channel"},
		}, {
			name:            "permissive: a refusal is counted but the caller is not told",
			paramConfigured: true,
			cfg:             paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModePermissive},
			silent:          true,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             write(modelName),
			wantStatus:      http.StatusOK,
			wantWarnings:    []string{notAllowed},
		}, {
			name:            "permissive: a malformed capability is counted but the caller is not told",
			paramConfigured: true,
			cfg:             paramConfig{Prefixes: []string{testParamPrefix}, Mode: ModePermissive},
			silent:          true,
			caps:            []string{testParamPrefix + anything + ":wirte", testParamPrefix + anything},
			req:             read(modelName),
			wantStatus:      http.StatusOK,
			wantWarnings:    []string{malformed},
		}, {
			name:            "the default mode is permissive: a refusal is only counted",
			paramConfigured: true,
			cfg:             paramConfig{Prefixes: []string{testParamPrefix}},
			silent:          true,
			caps:            []string{testParamPrefix + deviceInfo + ":read"},
			req:             write(modelName),
			wantStatus:      http.StatusOK,
			wantWarnings:    []string{notAllowed},
		}, {
			name:            "an override replaces the token's capabilities",
			paramConfigured: true,
			cfg: paramConfig{
				Prefixes:  []string{testParamPrefix},
				Mode:      ModeEnforce,
				Overrides: map[string][]string{testPrincipal: {deviceInfo + ":read"}},
			},
			caps:       []string{testParamPrefix + anything},
			req:        write(modelName),
			wantStatus: http.StatusForbidden,
		}, {
			name:            "an override applies to a token with no parameter capability",
			paramConfigured: true,
			cfg: paramConfig{
				Prefixes:  []string{testParamPrefix},
				Mode:      ModeEnforce,
				Overrides: map[string][]string{testPrincipal: {deviceInfo + ":read"}},
			},
			req:        read("Device.WiFi.Radio.1.Channel"),
			wantStatus: http.StatusForbidden,
		}, {
			name:            "an empty override makes the caller unrestricted",
			paramConfigured: true,
			cfg: paramConfig{
				Prefixes:  []string{testParamPrefix},
				Mode:      ModeEnforce,
				Overrides: map[string][]string{testPrincipal: {}},
			},
			caps:       []string{testParamPrefix + "Device.Nothing:read"},
			req:        write(modelName),
			wantStatus: http.StatusOK,
		}, {
			name:            "an override matches the principal without regard to case",
			paramConfigured: true,
			cfg: paramConfig{
				Prefixes:  []string{testParamPrefix},
				Mode:      ModeEnforce,
				Overrides: map[string][]string{strings.ToUpper(testPrincipal): {deviceInfo + ":read"}},
			},
			caps:       []string{testParamPrefix + anything},
			req:        write(modelName),
			wantStatus: http.StatusForbidden,
		}, {
			name:            "an override for another caller does not apply",
			paramConfigured: true,
			cfg: paramConfig{
				Prefixes:  []string{testParamPrefix},
				Mode:      ModeEnforce,
				Overrides: map[string][]string{"someone-else": {"Device.Nothing:read"}},
			},
			caps:       []string{testParamPrefix + anything},
			req:        write(modelName),
			wantStatus: http.StatusOK,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			v.Set(jwtPrefixesKey, []string{testPrefix})
			if tc.paramConfigured {
				v.Set(paramPrefixesKey, tc.cfg.Prefixes)
			}

			cfg := inboundConfig{JWT: jwtConfig{Prefixes: []string{testPrefix}, Param: tc.cfg}}
			extract := func(*http.Request) (ParamRequest, bool) { return tc.req, !tc.nothingToCheck }
			mw, checks, warnings := protectParams(t, cfg, v, keys.provider, extract)

			rec := serveRequest(t, mw, func(req *http.Request) {
				req.Header.Set("Authorization", token(tc.caps...))
			})

			assert.Equal(t, tc.wantStatus, rec.Code)
			assert.Equal(t, tc.wantStatus == http.StatusOK, reached(rec))

			if tc.wantRejectedFor != "" {
				assert.Contains(t, rec.Header().Values(WarningHeader), rejected+"; op="+string(tc.req.Operation)+"; name="+tc.wantRejectedFor)
			}

			// a refusal is explained to the caller after any warnings
			wantHeaders := tc.wantWarnings
			if tc.wantStatus == http.StatusForbidden {
				wantHeaders = append(append([]string(nil), tc.wantWarnings...), rejected+"; op="+string(tc.req.Operation)+"; name=")
			}

			got := rec.Header().Values(WarningHeader)
			if tc.silent {
				assert.Empty(t, got, "a silent mode writes no warning header")
			} else {
				require.Len(t, got, len(wantHeaders), "warnings: %v", got)
				for i, want := range wantHeaders {
					assert.True(t, strings.HasPrefix(got[i], want), "warning %q should start with %q", got[i], want)
				}
			}

			// every warning is counted, under its own reason, told or not;
			// the explanation of a refusal is not a warning
			assert.Equal(t, len(tc.wantWarnings), testutil.CollectAndCount(warnings))

			if tc.wantStatus == http.StatusForbidden {
				rejected := checks.With(prometheus.Labels{
					OutcomeLabel:   Rejected,
					ReasonLabel:    ParameterNotAllowed,
					ClientIDLabel:  testPrincipal,
					PartnerIDLabel: testPartner,
					EndpointLabel:  NoneEndpoint,
					MethodLabel:    http.MethodGet,
				})
				assert.Equal(t, 1.0, testutil.ToFloat64(rejected))
			}
		})
	}
}

func TestParamCheck_NilChecksNothing(t *testing.T) {
	var check *ParamCheck

	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) })
	rec := serveRequest(t, check.Middleware(nil)(next), nil)
	assert.Equal(t, http.StatusTeapot, rec.Code)
}

func TestParamCheck_NeedsAnExtractor(t *testing.T) {
	check, err := newParamCheck(paramConfig{Prefixes: []string{testParamPrefix}}, zap.NewNop(), testCounter(), testWarningCounter(), nil)
	require.NoError(t, err)

	assert.Panics(t, func() { check.Middleware(nil)(http.NotFoundHandler()) })
}

func TestParamCheck_CacheIsBounded(t *testing.T) {
	check, err := newParamCheck(paramConfig{Prefixes: []string{testParamPrefix}, CacheSize: 2}, zap.NewNop(), testCounter(), testWarningCounter(), nil)
	require.NoError(t, err)

	for _, capability := range []string{"a", testParamPrefix + "Device.A", testParamPrefix + "Device.B", testParamPrefix + "Device.C"} {
		check.parse(capability)
		assert.LessOrEqual(t, len(check.cache), 2)
	}

	// a cached capability parses the same way again
	first := check.parse(testParamPrefix + "Device.C")
	assert.Equal(t, first, check.parse(testParamPrefix+"Device.C"))
	assert.True(t, first.selected)
	assert.False(t, check.parse("a").selected)
}

func TestParseParamCapability(t *testing.T) {
	cases := []struct {
		text              string
		wantRead          bool
		wantWrite         bool
		wantSubtree       bool
		wantErr           bool
		matches, rejected []string
	}{
		{text: anything, wantRead: true, wantWrite: true, wantSubtree: true, matches: []string{"Device.", "device.x.y"}},
		{text: anything + ":all", wantRead: true, wantWrite: true, wantSubtree: true},
		{text: anything + ":read", wantRead: true, wantSubtree: true},
		{text: anything + ":write", wantWrite: true, wantSubtree: true},
		{text: "Device.*:read", wantRead: true, matches: []string{"Device.X"}, rejected: []string{"Device.X.Y"}},
		{text: "Device.Foo.**.Bar", wantRead: true, wantWrite: true, matches: []string{"Device.Foo.1.Bar"}, rejected: []string{"Device.Foo.Bar"}},
		{text: anything + ":READ", wantErr: true},
		{text: malformedAnything, wantErr: true},
		{text: ":read", wantErr: true},
		{text: "", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			got, err := parseParamCapability(tc.text)
			if tc.wantErr {
				assert.Error(t, err)
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.wantRead, got.read, "read")
			assert.Equal(t, tc.wantWrite, got.write, "write")
			assert.Equal(t, tc.wantSubtree, got.subtree, "subtree")
			for _, name := range tc.matches {
				assert.True(t, got.pattern.MatchString(strings.ToLower(name)), name)
			}
			for _, name := range tc.rejected {
				assert.False(t, got.pattern.MatchString(strings.ToLower(name)), name)
			}
		})
	}
}

// The param keys are documented in tr1d1um.yaml; this checks that they land
// on the right fields.
func TestParamConfigFromYAML(t *testing.T) {
	const config = `
auth:
  inbound:
    jwt:
      prefixes: ["x1:webpa:api:"]
      param:
        prefixes: ["x1:webpa:param:"]
        mode: enforce
        cacheSize: 256
        overrides:
          partner-x: ["Device.DeviceInfo.**:read", "Device.WiFi.SSID.*.SSID"]
          exempt: []
`

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(config)))

	var cfg inboundConfig
	require.NoError(t, v.UnmarshalKey(inboundConfigKey, &cfg))

	assert.True(t, v.IsSet(paramConfigKey))
	assert.Equal(t, []string{"x1:webpa:param:"}, cfg.JWT.Param.Prefixes)
	assert.Equal(t, ModeEnforce, cfg.JWT.Param.Mode)
	assert.Equal(t, 256, cfg.JWT.Param.CacheSize)
	assert.Equal(t, []string{"Device.DeviceInfo.**:read", "Device.WiFi.SSID.*.SSID"}, cfg.JWT.Param.Overrides["partner-x"])
	exempt, ok := cfg.JWT.Param.Overrides["exempt"]
	assert.True(t, ok)
	assert.Empty(t, exempt)
}
