// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
)

func TestMetricsRefreshDatabaseHealth(t *testing.T) {
	var pingError error
	s := &server{
		telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil))),
		databasePing: func(ctx context.Context) error {
			deadline, ok := ctx.Deadline()
			if !ok || time.Until(deadline) > 3*time.Second {
				t.Fatal("database ping lacks the readiness timeout")
			}
			return pingError
		},
	}
	metrics := s.metricsHandler()
	scrape := func(want string) {
		t.Helper()
		w := httptest.NewRecorder()
		metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if w.Code != http.StatusOK {
			t.Fatalf("metrics status = %d", w.Code)
		}
		body := w.Body.String()
		if !strings.Contains(body, "tairouter_database_up "+want+"\n") || !strings.Contains(body, "tairouter_health 1\n") {
			t.Fatalf("database or unrelated health metric missing from scrape: %s", body)
		}
		if strings.Contains(body, "private-database-detail") {
			t.Fatal("database error leaked into metrics")
		}
	}

	// Fresh telemetry must report healthy storage without a readiness request.
	scrape("1")
	scrape("1")
	ready := httptest.NewRecorder()
	s.readyHandler(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK || ready.Body.String() != "ready\n" {
		t.Fatalf("healthy readiness response = %d %q", ready.Code, ready.Body.String())
	}
	pingError = errors.New("private-database-detail")
	scrape("0")
	ready = httptest.NewRecorder()
	s.readyHandler(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable || ready.Body.String() != "database unavailable\n" {
		t.Fatalf("failed readiness response = %d %q", ready.Code, ready.Body.String())
	}
	pingError = nil
	scrape("1")
}

func TestMetricsRefreshWithPostgreSQLStore(t *testing.T) {
	s := &server{store: serverStore(t), telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil)))}
	metrics := s.metricsHandler()
	scrape := func(want string) {
		t.Helper()
		w := httptest.NewRecorder()
		metrics.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "tairouter_database_up "+want+"\n") {
			t.Fatalf("database metric after ping: status=%d body=%s", w.Code, w.Body.String())
		}
	}
	scrape("1")
	if err := s.store.Close(); err != nil {
		t.Fatal(err)
	}
	scrape("0")
	// Reopen an independent pool: a closed pool cannot prove recovery.
	s.store = serverStore(t)
	scrape("1")
}

func TestMetricsDatabaseTimeoutAndReadinessResponse(t *testing.T) {
	s := &server{
		telemetry: observability.New(slog.New(slog.NewJSONHandler(io.Discard, nil))),
		databasePing: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	w := httptest.NewRecorder()
	s.metricsHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/metrics", nil).WithContext(ctx))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "tairouter_database_up 0\n") {
		t.Fatalf("canceled ping made metrics unavailable: status=%d body=%s", w.Code, w.Body.String())
	}
}
