package server

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/rpc"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	statuspb "google.golang.org/genproto/googleapis/rpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

const correlationHeader = "x-devhud-correlation-id"

const (
	grpcStatusHeader        = "Grpc-Status"
	grpcMessageHeader       = "Grpc-Message"
	grpcStatusDetailsHeader = "Grpc-Status-Details-Bin"
)

type requestMetrics struct {
	requests metric.Int64Counter
	duration metric.Float64Histogram
}

func recoverConnectPanics(logger *slog.Logger) connect.HandlerOption {
	return connect.WithRecover(func(ctx context.Context, specification connect.Spec, _ http.Header, _ any) error {
		logger.ErrorContext(ctx, "Connect RPC panic recovered",
			"correlation_id", rpc.CorrelationID(ctx),
			"procedure", safeProcedure(specification.Procedure),
		)
		return rpc.NewError(connect.CodeInternal, "internal service error", rpc.CorrelationID(ctx))
	})
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

func connectErrorMetadata(connectPaths map[string]struct{}, next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if _, ok := connectPaths[request.URL.Path]; !ok {
			next.ServeHTTP(response, request)
			return
		}
		writer := &connectErrorResponseWriter{ResponseWriter: response}
		next.ServeHTTP(writer, request)
		writer.flush(rpc.CorrelationID(request.Context()))
	})
}

type connectErrorResponseWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
	buffering   bool
	body        bytes.Buffer
}

func (w *connectErrorResponseWriter) WriteHeader(status int) {
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	w.status = status
	w.buffering = shouldBufferConnectResponse(status, w.Header().Get("Content-Type"))
	if !w.buffering {
		w.ResponseWriter.WriteHeader(status)
	}
}

func (w *connectErrorResponseWriter) Write(data []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		return w.body.Write(data)
	}
	return w.ResponseWriter.Write(data)
}

func (w *connectErrorResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.buffering {
		return
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (w *connectErrorResponseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

func (w *connectErrorResponseWriter) flush(correlationID string) {
	if !w.wroteHeader && shouldBufferConnectResponse(http.StatusOK, w.Header().Get("Content-Type")) {
		w.WriteHeader(http.StatusOK)
	}
	if !w.buffering {
		return
	}
	data := w.body.Bytes()
	contentType := w.Header().Get("Content-Type")
	if strings.HasPrefix(contentType, "application/grpc") {
		addGRPCErrorMetadata(w.Header(), correlationID)
	} else {
		data = addConnectJSONErrorMetadata(data, correlationID)
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	}
	w.ResponseWriter.WriteHeader(w.status)
	_, _ = w.ResponseWriter.Write(data)
}

func shouldBufferConnectResponse(status int, contentType string) bool {
	return strings.HasPrefix(contentType, "application/grpc") ||
		(status >= http.StatusBadRequest && strings.HasPrefix(contentType, "application/json"))
}

func addConnectJSONErrorMetadata(data []byte, correlationID string) []byte {
	var wireError connectWireError
	if correlationID != "" && json.Unmarshal(data, &wireError) == nil && !wireError.hasDetail("devhud.v1.ErrorMetadata") {
		metadata := &devhudv1.ErrorMetadata{CorrelationId: &devhudv1.UuidV7{Value: correlationID}}
		if detail, err := connect.NewErrorDetail(metadata); err == nil {
			wireError.Details = append(wireError.Details, connectWireDetail{
				Type:  detail.Type(),
				Value: base64.RawStdEncoding.EncodeToString(detail.Bytes()),
			})
			if encoded, err := json.Marshal(wireError); err == nil {
				data = encoded
			}
		}
	}
	return data
}

func addGRPCErrorMetadata(header http.Header, correlationID string) {
	if correlationID == "" {
		return
	}
	statusValue, statusTrailer := grpcHeaderValue(header, grpcStatusHeader)
	if statusValue == "" || statusValue == "0" {
		return
	}

	var status statuspb.Status
	detailsValue, _ := grpcHeaderValue(header, grpcStatusDetailsHeader)
	if detailsValue != "" {
		details, err := connect.DecodeBinaryHeader(detailsValue)
		if err != nil || proto.Unmarshal(details, &status) != nil {
			return
		}
	} else {
		code, err := strconv.ParseInt(statusValue, 10, 32)
		if err != nil {
			return
		}
		message, err := url.PathUnescape(grpcHeaderValueOnly(header, grpcMessageHeader))
		if err != nil {
			return
		}
		status.Code = int32(code)
		status.Message = message
	}
	for _, detail := range status.Details {
		if detail.GetTypeUrl() == "type.googleapis.com/devhud.v1.ErrorMetadata" {
			return
		}
	}
	metadata, err := anypb.New(&devhudv1.ErrorMetadata{CorrelationId: &devhudv1.UuidV7{Value: correlationID}})
	if err != nil {
		return
	}
	status.Details = append(status.Details, metadata)
	encoded, err := proto.Marshal(&status)
	if err != nil {
		return
	}
	key := grpcStatusDetailsHeader
	if statusTrailer {
		key = http.TrailerPrefix + key
	}
	header.Set(key, connect.EncodeBinaryHeader(encoded))
}

func grpcHeaderValue(header http.Header, key string) (string, bool) {
	if value := header.Get(http.TrailerPrefix + key); value != "" {
		return value, true
	}
	return header.Get(key), false
}

func grpcHeaderValueOnly(header http.Header, key string) string {
	value, _ := grpcHeaderValue(header, key)
	return value
}

type connectWireError struct {
	Code    string              `json:"code"`
	Message string              `json:"message,omitempty"`
	Details []connectWireDetail `json:"details,omitempty"`
}

func (e connectWireError) hasDetail(detailType string) bool {
	for _, detail := range e.Details {
		if detail.Type == detailType {
			return true
		}
	}
	return false
}

type connectWireDetail struct {
	Type  string          `json:"type"`
	Value string          `json:"value"`
	Debug json.RawMessage `json:"debug,omitempty"`
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
		if request.URL.Path == "/healthz" {
			return
		}
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
