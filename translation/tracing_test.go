// SPDX-FileCopyrightText: 2026 Comcast Cable Communications Management, LLC
// SPDX-License-Identifier: Apache-2.0

package translation

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/justinas/alice"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/xmidt-org/candlelight"
	"github.com/xmidt-org/tr1d1um/transaction"
	"github.com/xmidt-org/wrp-go/v3"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// contextCapturingService records the context the endpoint was called with.
type contextCapturingService struct {
	ctx context.Context
}

func (s *contextCapturingService) SendWRP(ctx context.Context, _ *wrp.Message, _ string) (*transaction.XmidtResponse, error) {
	s.ctx = ctx
	return &transaction.XmidtResponse{Code: http.StatusOK, ForwardedHeaders: http.Header{}}, nil
}

// TestConfigHandlerTracing checks that both device routes use the configured
// tracing rather than an empty one: trace context carried under the
// configured header prefix must reach the service in the request context.
func TestConfigHandlerTracing(t *testing.T) {
	const (
		headerPrefix = "X-Xmidt-Headers"
		traceID      = "4bf92f3577b34da6a3ce929d0e0e4736"
		spanID       = "00f067aa0ba902b7"
	)

	configured, err := candlelight.New(candlelight.Config{
		ApplicationName: "tr1d1um",
		Provider:        "noop",
		HeaderPrefix:    headerPrefix,
	})
	require.NoError(t, err)

	// serve sends a request carrying trace context under the header prefix
	// through a handler built with the given tracing, and returns the span
	// context the service saw.
	serve := func(t *testing.T, tracing candlelight.Tracing, method, path string) trace.SpanContext {
		t.Helper()

		service := &contextCapturingService{}
		router := mux.NewRouter()
		ConfigHandler(&Options{
			S:             service,
			APIRouter:     router,
			Authenticate:  &alice.Chain{},
			Log:           zap.NewNop(),
			ValidServices: []string{"config"},
			Tracing:       tracing,
		})

		req := httptest.NewRequest(method, path, nil)
		req.Header.Add(headerPrefix, "traceparent: 00-"+traceID+"-"+spanID+"-01")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		require.Equal(t, http.StatusOK, rec.Code)
		require.NotNil(t, service.ctx, "the service was not called")

		return trace.SpanContextFromContext(service.ctx)
	}

	routes := []struct {
		name   string
		method string
		path   string
	}{
		{name: "service route", method: http.MethodGet, path: "/device/mac:112233445566/config?names=Device.X"},
		{name: "parameter route", method: http.MethodDelete, path: "/device/mac:112233445566/config/Device.Table.1."},
	}

	for _, route := range routes {
		t.Run(route.name, func(t *testing.T) {
			span := serve(t, configured, route.method, route.path)
			assert.Equal(t, traceID, span.TraceID().String())
			assert.Equal(t, spanID, span.SpanID().String())
		})
	}

	t.Run("empty tracing has no prefix and misses it", func(t *testing.T) {
		span := serve(t, candlelight.Tracing{}, routes[0].method, routes[0].path)
		assert.False(t, span.IsValid())
	})
}
