// SPDX-License-Identifier: Apache-2.0
package main

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type requestIDKey struct{}
type telemetry struct {
	registry                                          *prometheus.Registry
	health, database, cleanupFailures, cleanupSuccess prometheus.Gauge
	requests                                          *prometheus.CounterVec
	events                                            *prometheus.CounterVec
	logger                                            *slog.Logger
}

func newTelemetry(logger *slog.Logger) *telemetry {
	t := &telemetry{registry: prometheus.NewRegistry(), logger: logger}
	t.health = prometheus.NewGauge(prometheus.GaugeOpts{Name: "aiprouter_health", Help: "Service process is healthy."})
	t.database = prometheus.NewGauge(prometheus.GaugeOpts{Name: "aiprouter_database_up", Help: "Last PostgreSQL connectivity check succeeded."})
	t.cleanupFailures = prometheus.NewGauge(prometheus.GaugeOpts{Name: "aiprouter_cleanup_consecutive_failures", Help: "Consecutive failed cleanup runs."})
	t.cleanupSuccess = prometheus.NewGauge(prometheus.GaugeOpts{Name: "aiprouter_cleanup_last_success_timestamp_seconds", Help: "Last successful cleanup, initialized at startup for grace."})
	t.requests = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aiprouter_http_requests_total", Help: "Requests by bounded route family."}, []string{"route"})
	t.events = prometheus.NewCounterVec(prometheus.CounterOpts{Name: "aiprouter_inference_events_total", Help: "Inference outcomes without request content."}, []string{"event"})
	t.registry.MustRegister(t.health, t.database, t.cleanupFailures, t.cleanupSuccess, t.requests, t.events)
	t.health.Set(1)
	t.cleanupSuccess.SetToCurrentTime()
	return t
}
func (t *telemetry) Event(ctx context.Context, event string) {
	switch event {
	case "cost_unknown", "admission_release_failed", "upstream_send", "continuation_write_failed", "stream_failed", "usage_write_failed", "usage_unknown", "usage_recorded", "request_canceled", "rate_limited", "upstream_error", "gateway_error":
	default:
		event = "other"
	}
	t.events.WithLabelValues(event).Inc()
	if event == "usage_write_failed" {
		t.logger.ErrorContext(ctx, "usage recording failed", "request_id", requestID(ctx))
	}
}
func requestID(ctx context.Context) string { s, _ := ctx.Value(requestIDKey{}).(string); return s }
func routeFamily(path string) string {
	switch {
	case path == "/healthz":
		return "health"
	case path == "/readyz":
		return "ready"
	case path == "/metrics":
		return "metrics"
	case strings.HasPrefix(path, "/auth/"):
		return "auth"
	case strings.HasPrefix(path, "/aiprouter.v1."):
		return "management"
	case strings.HasPrefix(path, "/v1/"):
		return "inference"
	default:
		return "other"
	}
}
func (t *telemetry) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomToken()
		ctx := context.WithValue(r.Context(), requestIDKey{}, id)
		family := routeFamily(r.URL.Path)
		ctx, span := otel.Tracer("aiprouter-server").Start(ctx, family)
		defer span.End()
		span.SetAttributes(attribute.String("request.id", id), attribute.String("http.request.method", r.Method))
		w.Header().Set("X-Request-ID", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		t.requests.WithLabelValues(family).Inc()
		start := time.Now()
		defer func() {
			t.logger.InfoContext(ctx, "request completed", "request_id", id, "route", family, "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (t *telemetry) metrics() http.Handler {
	return promhttp.HandlerFor(t.registry, promhttp.HandlerOpts{})
}
func setupTracing(ctx context.Context, enabled bool) (func(context.Context) error, error) {
	options := []sdktrace.TracerProviderOption{sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", "aiprouter-server")))}
	if enabled {
		exporter, e := otlptracehttp.New(ctx)
		if e != nil {
			return nil, e
		}
		options = append(options, sdktrace.WithBatcher(exporter))
	}
	provider := sdktrace.NewTracerProvider(options...)
	otel.SetTracerProvider(provider)
	return provider.Shutdown, nil
}
