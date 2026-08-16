package server

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/rpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

const correlationHeader = "x-devhud-correlation-id"

type requestMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func newRequestMetrics() (requestMetrics, error) {
	meter := otel.Meter("github.com/delinoio/oss/servers/devhud-api")
	requests, err := meter.Int64Counter("devhud_api_requests", metric.WithUnit("{request}"))
	if err != nil {
		return requestMetrics{}, err
	}
	duration, err := meter.Float64Histogram("devhud_api_request_duration", metric.WithUnit("ms"))
	if err != nil {
		return requestMetrics{}, err
	}
	return requestMetrics{requests: requests, duration: duration}, nil
}

func correlation(ids domain.IDGenerator, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		id, err := ids.New()
		if err != nil {
			http.Error(response, "unable to create request metadata", http.StatusInternalServerError)
			return
		}
		response.Header().Set(correlationHeader, id)
		next.ServeHTTP(response, request.WithContext(rpc.WithCorrelationID(request.Context(), id)))
	})
}

func recoverPanics(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				logger.ErrorContext(request.Context(), "request panic recovered", "correlation_id", rpc.CorrelationID(request.Context()))
				http.Error(response, "internal service error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(response, request)
	})
}

func observeRequests(logger *slog.Logger, repository domain.Repository, clock domain.Clock, ids domain.IDGenerator, metrics requestMetrics, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := clock.Now()
		status := &statusWriter{ResponseWriter: response, status: http.StatusOK}
		next.ServeHTTP(status, request)
		duration := clock.Now().Sub(started)
		procedure := safeProcedure(request.URL.Path)
		attributes := metric.WithAttributes(
			attribute.String("rpc.procedure", procedure),
			attribute.Int("http.response.status_code", status.status),
		)
		metrics.requests.Add(request.Context(), 1, attributes)
		metrics.duration.Record(request.Context(), float64(duration.Milliseconds()), attributes)
		logger.InfoContext(request.Context(), "request completed",
			"correlation_id", rpc.CorrelationID(request.Context()),
			"procedure", procedure,
			"http_status", status.status,
			"duration_ms", duration.Milliseconds(),
		)
		requestLogID, err := ids.New()
		if err != nil {
			return
		}
		logContext, cancel := context.WithTimeout(context.WithoutCancel(request.Context()), time.Second)
		defer cancel()
		if err := repository.RecordRequest(logContext, domain.RequestLog{
			ID:                   requestLogID,
			CorrelationID:        rpc.CorrelationID(request.Context()),
			Procedure:            procedure,
			HTTPStatus:           status.status,
			DurationMilliseconds: max(duration.Milliseconds(), 0),
			CreatedAt:            started,
			ExpiresAt:            started.Add(domain.RequestLogRetention),
		}); err != nil {
			logger.WarnContext(request.Context(), "request metadata persistence failed", "correlation_id", rpc.CorrelationID(request.Context()))
		}
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func safeProcedure(path string) string {
	switch path {
	case "/devhud.v1.BootstrapService/GetBootstrap",
		"/devhud.v1.SettingsService/GetSettings",
		"/devhud.v1.SettingsService/ReplaceSettings",
		"/devhud.v1.AccountService/GetAccount",
		"/devhud.v1.AccountService/DeleteAccount",
		"/devhud.v1.AccountService/RestoreAccount",
		"/healthz", "/readyz", "/metrics":
		return path
	default:
		return "unmatched"
	}
}
