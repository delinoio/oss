// SPDX-License-Identifier: Apache-2.0
package observability_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/delinoio/oss/servers/tairouter/internal/observability"
)

func TestHealthAndRedactedMetrics(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	metrics := observability.New(logger)
	metrics.Event(context.Background(), "secret-token-content")
	w := httptest.NewRecorder()
	metrics.Metrics().ServeHTTP(w, httptest.NewRequest("GET", "/metrics", nil))
	if strings.Contains(w.Body.String(), "secret-token-content") {
		t.Fatal("unbounded metric label")
	}
	for _, name := range []string{"tairouter_health", "tairouter_database_up", "tairouter_cleanup_consecutive_failures", "tairouter_cleanup_last_success_timestamp_seconds"} {
		if !strings.Contains(w.Body.String(), name) {
			t.Fatalf("missing metric %s", name)
		}
	}
}

// Request IDs must cross the package boundary through context and match the
// response header. Request paths and query secrets must not enter telemetry.
func TestMiddlewareRequestIdentityAndRedaction(t *testing.T) {
	var logs bytes.Buffer
	telemetry := observability.New(slog.New(slog.NewJSONHandler(&logs, nil)))
	var ids []string
	handler := telemetry.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := observability.RequestID(r.Context())
		if id == "" || id != w.Header().Get("X-Request-ID") {
			t.Fatal("request context and response identity differ")
		}
		ids = append(ids, id)
		telemetry.Event(r.Context(), "usage_write_failed")
		w.WriteHeader(http.StatusNoContent)
	}))
	for range 2 {
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/v1/private-value?token=secret-value", nil))
	}
	if ids[0] == ids[1] {
		t.Fatal("request IDs were reused")
	}
	if observability.RequestID(context.Background()) != "" {
		t.Fatal("request identity leaked outside context")
	}
	metrics := httptest.NewRecorder()
	telemetry.Metrics().ServeHTTP(metrics, httptest.NewRequest("GET", "/metrics", nil))
	if !strings.Contains(metrics.Body.String(), `tairouter_http_requests_total{route="inference"} 2`) {
		t.Fatal("requests did not use a bounded route family")
	}
	for _, output := range []string{logs.String(), metrics.Body.String()} {
		if strings.Contains(output, "private-value") || strings.Contains(output, "secret-value") {
			t.Fatal("request content entered instrumentation")
		}
	}
	for _, id := range ids {
		if !strings.Contains(logs.String(), id) {
			t.Fatal("request log lost its identity")
		}
	}
}
