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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
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
				assert.Error(t, err)
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
