// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xmidt-org/ancla"
	"github.com/xmidt-org/ancla/schema"
	webhook "github.com/xmidt-org/webhook-schema"

	"github.com/xmidt-org/tr1d1um/transaction"
)

// temporaryError is an error that reports itself as temporary, as a network
// timeout does.
type temporaryError struct{}

func (temporaryError) Error() string   { return "temporary" }
func (temporaryError) Temporary() bool { return true }

// scriptedTransport answers each request with the next error in errs, and
// with a 200 once errs runs out.  It records the body of every request.
type scriptedTransport struct {
	errs   []error
	bodies []string
}

func (st *scriptedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	var body []byte
	if r.Body != nil {
		body, _ = io.ReadAll(r.Body)
	}
	st.bodies = append(st.bodies, string(body))

	if len(st.errs) > 0 {
		err := st.errs[0]
		st.errs = st.errs[1:]
		return nil, err
	}

	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(bytes.NewReader(nil)),
		Request:    r,
	}, nil
}

func TestNewRetryingClient(t *testing.T) {
	tests := []struct {
		desc        string
		retries     int
		errs        []error
		wantErr     bool
		wantSent    int
		wantRetried float64
	}{
		{
			desc:     "success needs no retry",
			retries:  2,
			wantSent: 1,
		}, {
			desc:        "temporary errors are retried",
			retries:     2,
			errs:        []error{temporaryError{}, temporaryError{}},
			wantSent:    3,
			wantRetried: 2,
		}, {
			desc:        "retries stop at the limit",
			retries:     2,
			errs:        []error{temporaryError{}, temporaryError{}, temporaryError{}},
			wantErr:     true,
			wantSent:    3,
			wantRetried: 2,
		}, {
			desc:     "other errors are not retried",
			retries:  2,
			errs:     []error{errors.New("permanent")},
			wantErr:  true,
			wantSent: 1,
		}, {
			desc:     "no retries when disabled",
			retries:  0,
			errs:     []error{temporaryError{}},
			wantErr:  true,
			wantSent: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			transport := &scriptedTransport{errs: tc.errs}
			counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_retries"})

			client, err := newRetryingClient(zap.NewNop(), tc.retries, time.Millisecond, counter,
				&http.Client{Transport: transport})
			require.NoError(t, err)

			req, err := http.NewRequest(http.MethodPost, "http://example.com/device", bytes.NewBufferString("payload"))
			require.NoError(t, err)

			resp, err := client.Do(req)
			if tc.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, http.StatusOK, resp.StatusCode)
				resp.Body.Close()
			}

			require.Len(t, transport.bodies, tc.wantSent)
			for _, body := range transport.bodies {
				assert.Equal(t, "payload", body, "every attempt sends the whole body")
			}
			assert.Equal(t, tc.wantRetried, testutil.ToFloat64(counter))
		})
	}
}

// TestNewRetryingClient_DefaultInterval checks that an unset interval falls
// back to defaultRetryInterval.  To the retry package a nonpositive interval
// means never retry, so without the default a configured retry count would
// silently do nothing.
func TestNewRetryingClient_DefaultInterval(t *testing.T) {
	t.Parallel()

	for _, interval := range []time.Duration{0, -time.Second} {
		t.Run(interval.String(), func(t *testing.T) {
			t.Parallel()

			transport := &scriptedTransport{errs: []error{temporaryError{}}}
			counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_retries"})

			client, err := newRetryingClient(zap.NewNop(), 1, interval, counter, &http.Client{Transport: transport})
			require.NoError(t, err)

			req, err := http.NewRequest(http.MethodGet, "http://example.com/device", nil)
			require.NoError(t, err)

			start := time.Now()
			resp, err := client.Do(req)
			elapsed := time.Since(start)
			require.NoError(t, err, "the temporary error is retried")
			resp.Body.Close()

			assert.Len(t, transport.bodies, 2)
			assert.Equal(t, float64(1), testutil.ToFloat64(counter))
			assert.GreaterOrEqual(t, elapsed, defaultRetryInterval, "the retry waits the default interval")
		})
	}
}

func TestProvideWebhookHandlers(t *testing.T) {
	t.Run("disabled when not configured", func(t *testing.T) {
		out, err := provideWebhookHandlers(provideWebhookHandlersIn{V: viper.New(), Logger: zap.NewNop()})
		require.NoError(t, err)
		assert.Nil(t, out.AddWebhookHandler)
		assert.Nil(t, out.V2AddWebhookHandler)
		assert.Nil(t, out.GetAllWebhooksHandler)
	})

	enabled := func() *viper.Viper {
		v := viper.New()
		v.Set("webhook", map[string]any{})
		return v
	}

	t.Run("enabled", func(t *testing.T) {
		out, err := provideWebhookHandlers(provideWebhookHandlersIn{V: enabled(), Logger: zap.NewNop()})
		require.NoError(t, err)
		assert.NotNil(t, out.AddWebhookHandler)
		assert.NotNil(t, out.V2AddWebhookHandler)
		assert.NotNil(t, out.GetAllWebhooksHandler)
	})

	t.Run("invalid validation config", func(t *testing.T) {
		var cfg ancla.Config
		cfg.Validation.IP.ForbiddenSubnets = []string{"not a subnet"}

		_, err := provideWebhookHandlers(provideWebhookHandlersIn{V: enabled(), WebhookConfig: cfg, Logger: zap.NewNop()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to initialize webhook validators")
	})
}

func TestValidateWRPSource(t *testing.T) {
	for _, source := range []string{"dns:localhost", "dns:tr1d1um.example.com", "mac:112233445566", "event:device-status/foo"} {
		require.NoError(t, validateWRPSource(source), source)
	}

	for _, source := range []string{"", "tr1d1um", "not a locator"} {
		err := validateWRPSource(source)
		require.Error(t, err, source)
		assert.Contains(t, err.Error(), "WRPSource", source)
	}
}

func TestRequireReceiverURL(t *testing.T) {
	var v1 webhook.RegistrationV1
	require.ErrorIs(t, requireReceiverURL{}.Validate(&v1), errNoReceiverURL)
	v1.Config.ReceiverURL = "http://listener.example.test/events"
	require.NoError(t, requireReceiverURL{}.Validate(&v1))

	var v2 webhook.RegistrationV2
	require.NoError(t, requireReceiverURL{}.Validate(&v2), "no webhooks, nothing to require")
	v2.Webhooks = []webhook.Webhook{{}}
	require.ErrorIs(t, requireReceiverURL{}.Validate(&v2), errNoReceiverURL)
	v2.Webhooks[0].ReceiverURLs = []string{"http://listener.example.test/events"}
	require.NoError(t, requireReceiverURL{}.Validate(&v2))

	require.NoError(t, requireReceiverURL{}.Validate("not a registration"))
	assert.Equal(t, "requireReceiverURL()", requireReceiverURL{}.String())
}

func TestV2WebhookValidators(t *testing.T) {
	var cfg ancla.Config
	cfg.Validation.BuildOpts = schema.BuildOptions{
		AtLeastOneEvent:              true,
		ValidateRegistrationDuration: true,
		CheckUntil:                   true,
	}
	cfg.Validation.TTL.Max = time.Minute

	opts, err := v2WebhookValidators(cfg)
	require.NoError(t, err)

	names := make([]string, 0, len(opts))
	for _, o := range opts {
		names = append(names, o.String())
	}
	assert.Contains(t, names, "AtLeastOneEvent()")
	assert.Contains(t, names, "requireReceiverURL()")
	for _, name := range names {
		assert.NotContains(t, name, "ValidateRegistrationDuration", "the v2 shim repairs the duration instead")
		assert.NotContains(t, name, "untilOption", "the v2 shim repairs until instead")
	}

	cfg.Validation.IP.ForbiddenSubnets = []string{"not a subnet"}
	_, err = v2WebhookValidators(cfg)
	require.Error(t, err)
}

func TestV3WebhookValidators(t *testing.T) {
	cfg := ancla.Config{Validation: schema.SchemaURLValidatorConfig{
		URL:    schema.URLVConfig{Schemes: []string{"http", "https"}},
		Domain: schema.DomainVConfig{AllowSpecialUseDomains: true},
		TTL:    schema.TTLVConfig{Max: time.Minute},
		BuildOpts: schema.BuildOptions{
			AtLeastOneEvent:                true,
			EventRegexMustCompile:          true,
			DeviceIDRegexMustCompile:       true,
			ValidateRegistrationDuration:   true,
			ProvideReceiverURLValidator:    true,
			ProvideFailureURLValidator:     true,
			ProvideAlternativeURLValidator: true,
			CheckUntil:                     true,
		},
	}}

	opt, err := v3WebhookValidators(cfg)
	require.NoError(t, err)
	assert.Contains(t, opt.String(), "requireReceiverURL()")

	t.Run("v1", func(t *testing.T) {
		var r webhook.RegistrationV1
		r.Config.ReceiverURL = "http://listener.example.test/events"
		r.Events = []string{"device-status"}
		r.Duration = webhook.CustomDuration(30 * time.Second)
		require.NoError(t, opt.Validate(&r))

		r.Events = nil
		err := opt.Validate(&r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot have zero events")
	})

	t.Run("v2 gets only the validations that fit its shape", func(t *testing.T) {
		r := webhook.RegistrationV2{
			CanonicalName: "test",
			Webhooks:      []webhook.Webhook{{ReceiverURLs: []string{"http://listener.example.test/events"}}},
			Expires:       time.Now().Add(30 * time.Second),
		}
		require.NoError(t, opt.Validate(&r))

		r.Expires = time.Time{}
		err := opt.Validate(&r)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already expired")
		assert.NotContains(t, err.Error(), "invalid type", "no validation refuses the v2 shape as the wrong type")

		r.Webhooks[0].ReceiverURLs = nil
		require.ErrorIs(t, opt.Validate(&r), errNoReceiverURL)
	})

	t.Run("unknown type", func(t *testing.T) {
		require.Error(t, opt.Validate("not a registration"))
	})

	cfg.Validation.IP.ForbiddenSubnets = []string{"not a subnet"}
	_, err = v3WebhookValidators(cfg)
	require.Error(t, err)
}

// TestNewRetryingClient_BodyReadableAfterDo checks that a response body can be
// read once Do has returned.  Before retry v0.0.9, the retrying client bound
// each attempt to a context the runner canceled as Do returned, which failed
// the read of any body not yet buffered with "context canceled".
func TestNewRetryingClient_BodyReadableAfterDo(t *testing.T) {
	const size = 209220

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(20 * time.Millisecond)
		_, _ = w.Write(bytes.Repeat([]byte("x"), size))
	}))
	defer srv.Close()

	for _, retries := range []int{0, 1, 2} {
		t.Run(strconv.Itoa(retries), func(t *testing.T) {
			counter := prometheus.NewCounter(prometheus.CounterOpts{Name: "test_retries"})
			client, err := newRetryingClient(zap.NewNop(), retries, time.Millisecond, counter, &http.Client{})
			require.NoError(t, err)

			tr := transaction.New(&transaction.Options{Do: client.Do, RequestTimeout: 10 * time.Second})
			req, err := http.NewRequest(http.MethodPost, srv.URL, bytes.NewBufferString("hi"))
			require.NoError(t, err)

			resp, err := tr.Transact(req)
			require.NoError(t, err)
			assert.Len(t, resp.Body, size)
		})
	}
}
