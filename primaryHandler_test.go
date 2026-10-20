// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/xmidt-org/ancla"
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
