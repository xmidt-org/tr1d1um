// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lestrrat-go/jwx/v4/jwa"
	"github.com/lestrrat-go/jwx/v4/jwt"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testBearerKey = []byte("0123456789abcdef0123456789abcdef")

// signTestBearer returns an HS256 token that expires at exp.
func signTestBearer(t *testing.T, exp time.Time) string {
	t.Helper()

	token, err := jwt.NewBuilder().Expiration(exp).Build()
	require.NoError(t, err)

	signed, err := jwt.Sign(token, jwt.WithKey(jwa.HS256(), testBearerKey))
	require.NoError(t, err)

	return string(signed)
}

func TestBearerDecorator_Decorate(t *testing.T) {
	tests := []struct {
		name    string
		exp     time.Duration
		buffer  time.Duration
		fetches int32
	}{
		{
			name:    "cached token is reused",
			exp:     time.Hour,
			fetches: 1,
		}, {
			name:    "token near expiry is refreshed",
			exp:     time.Minute,
			buffer:  time.Hour,
			fetches: 2,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			signed := signTestBearer(t, time.Now().Add(tc.exp))

			var fetches atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				fetches.Add(1)
				assert.Equal(t, "secret", r.Header.Get("X-Client-Secret"))
				fmt.Fprintf(w, `{"token":%q}`, signed)
			}))
			defer srv.Close()

			d := &bearerDecorator{
				url:          srv.URL,
				payloadField: "token",
				buffer:       tc.buffer,
				headers:      map[string]string{"X-Client-Secret": "secret"},
				httpClient:   srv.Client(),
				parseOpts:    []jwt.ParseOption{jwt.WithKey(jwa.HS256(), testBearerKey)},
			}

			// Every request is decorated, including the ones that fetch a token.
			for range 2 {
				req := httptest.NewRequest(http.MethodGet, "/", nil)
				require.NoError(t, d.Decorate(context.Background(), req))
				assert.Equal(t, "Bearer "+signed, req.Header.Get("Authorization"))
			}

			assert.Equal(t, tc.fetches, fetches.Load())
		})
	}
}

func TestBearerDecorator_DecorateErrors(t *testing.T) {
	noExp, err := jwt.Sign(jwt.New(), jwt.WithKey(jwa.HS256(), testBearerKey))
	require.NoError(t, err)

	tests := []struct {
		name   string
		status int
		body   string
	}{
		{name: "non 200", status: http.StatusUnauthorized},
		{name: "invalid json", body: `{`},
		{name: "missing field", body: `{}`},
		{name: "non string field", body: `{"token":1}`},
		{name: "unparsable token", body: `{"token":"nope"}`},
		{name: "no expiration", body: fmt.Sprintf(`{"token":%q}`, noExp)},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()

			d := &bearerDecorator{
				url:          srv.URL,
				payloadField: "token",
				httpClient:   srv.Client(),
				parseOpts:    []jwt.ParseOption{jwt.WithKey(jwa.HS256(), testBearerKey)},
			}

			req := httptest.NewRequest(http.MethodGet, "/", nil)
			require.Error(t, d.Decorate(context.Background(), req))
			assert.Empty(t, req.Header.Get("Authorization"))
		})
	}
}

func TestBearerDecorator_DecorateCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	d := &bearerDecorator{url: "http://127.0.0.1:0", httpClient: http.DefaultClient}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	assert.ErrorIs(t, d.Decorate(ctx, req), context.Canceled)
}

// TestBearerDecorator_FetchDecoratesCallerRequest is a regression test.
// Decorate used to reuse its req parameter for the token request, so any call
// that fetched a token (the first call, and each refresh) put the
// Authorization header on the token request and sent the caller's request
// out unauthenticated.  Here each decorated request is sent to a downstream
// server, which must see the bearer token every time a token is fetched.
func TestBearerDecorator_FetchDecoratesCallerRequest(t *testing.T) {
	// The token is already inside the refresh buffer, so every call fetches.
	signed := signTestBearer(t, time.Now().Add(time.Minute))

	var fetches atomic.Int32
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		assert.Empty(t, r.Header.Get("Authorization"), "the token request must not carry the bearer token")
		fmt.Fprintf(w, `{"token":%q}`, signed)
	}))
	defer issuer.Close()

	var received []string
	downstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		received = append(received, r.Header.Get("Authorization"))
	}))
	defer downstream.Close()

	d := &bearerDecorator{
		url:          issuer.URL,
		payloadField: "token",
		buffer:       time.Hour,
		httpClient:   issuer.Client(),
		parseOpts:    []jwt.ParseOption{jwt.WithKey(jwa.HS256(), testBearerKey)},
	}

	const calls = 3
	for range calls {
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, downstream.URL, nil)
		require.NoError(t, err)
		require.NoError(t, d.Decorate(req.Context(), req))

		resp, err := downstream.Client().Do(req)
		require.NoError(t, err)
		resp.Body.Close()
	}

	assert.Equal(t, int32(calls), fetches.Load(), "every call should have fetched a token")
	for i, got := range received {
		assert.Equal(t, "Bearer "+signed, got, "downstream request %d was unauthenticated", i+1)
	}
}

func TestNewDecorator(t *testing.T) {
	const key = "auth.outbound.fanout"

	tests := []struct {
		name    string
		set     []string
		cfg     decoratorConfig
		wantErr error
		want    Decorator
	}{
		{
			name: "none set",
		}, {
			name: "more than one set",
			set:  []string{key + ".JWT", key + ".Basic"},
			cfg:  decoratorConfig{Basic: "dXNlcjpwYXNz"},
		}, {
			name:    "jwt without credentials",
			set:     []string{key + ".JWT"},
			cfg:     decoratorConfig{JWT: bearerDecoratorConfig{AuthURL: "https://issuer.example.com"}},
			wantErr: ErrEmptyAuthCredentials,
		}, {
			name: "jwt without url",
			set:  []string{key + ".JWT"},
			cfg: decoratorConfig{JWT: bearerDecoratorConfig{
				RequestHeaders: map[string]string{"X-Client-Secret": "secret"},
			}},
			wantErr: ErrEmptyAuthURL,
		}, {
			name:    "basic without credentials",
			set:     []string{key + ".Basic"},
			wantErr: ErrEmptyAuthCredentials,
		}, {
			name: "basic",
			set:  []string{key + ".Basic"},
			cfg:  decoratorConfig{Basic: "dXNlcjpwYXNz"},
			want: &basicDecorator{value: "dXNlcjpwYXNz"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v := viper.New()
			for _, k := range tc.set {
				v.Set(k, "x")
			}

			d, err := NewDecorator(tc.cfg, v, key)
			if tc.want == nil {
				require.Error(t, err)
				if tc.wantErr != nil {
					require.ErrorIs(t, err, tc.wantErr)
				}
				assert.Nil(t, d)

				return
			}

			require.NoError(t, err)
			assert.Equal(t, tc.want, d)
		})
	}

	t.Run("jwt", func(t *testing.T) {
		v := viper.New()
		v.Set(key+".JWT", "x")

		d, err := NewDecorator(decoratorConfig{JWT: bearerDecoratorConfig{
			AuthURL:        "https://issuer.example.com",
			PayloadField:   "token",
			Timeout:        5 * time.Second,
			Buffer:         time.Minute,
			RequestHeaders: map[string]string{"X-Client-Secret": "secret"},
		}}, v, key)
		require.NoError(t, err)

		bearer, ok := d.(*bearerDecorator)
		require.True(t, ok, "got %T", d)
		assert.Equal(t, "https://issuer.example.com", bearer.url)
		assert.Equal(t, "token", bearer.payloadField)
		assert.Equal(t, time.Minute, bearer.buffer)
		assert.Equal(t, map[string]string{"X-Client-Secret": "secret"}, bearer.headers)
		assert.Equal(t, 5*time.Second, bearer.httpClient.Timeout)
	})
}

func TestBasicDecorator_Decorate(t *testing.T) {
	d := &basicDecorator{value: "dXNlcjpwYXNz"}
	req := httptest.NewRequest(http.MethodGet, "/", nil)

	require.NoError(t, d.Decorate(context.Background(), req))
	assert.Equal(t, "Basic dXNlcjpwYXNz", req.Header.Get("Authorization"))
}
