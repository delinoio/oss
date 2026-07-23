// Package requestmeta propagates safe request and trace correlation IDs across
// HTTP and Connect handlers.
package requestmeta

import (
	"context"
	"encoding/hex"
	"errors"
	"net/http"
	"reflect"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
)

const (
	RequestIDHeader   = "X-Request-Id"
	TraceIDHeader     = "X-Trace-Id"
	TraceparentHeader = "Traceparent"
)

var (
	requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
	traceIDPattern   = regexp.MustCompile(`^[0-9a-f]{32}$`)
)

// IDGenerator is injectable for deterministic middleware tests.
type IDGenerator interface {
	New() (uuid.UUID, error)
}

type defaultIDGenerator struct{}

func (defaultIDGenerator) New() (uuid.UUID, error) { return uuidv7.New() }

// Metadata is the safe correlation state carried in context.
type Metadata struct {
	RequestID string
	TraceID   string
}

type metadataContextKey struct{}

// WithMetadata attaches correlation metadata.
func WithMetadata(ctx context.Context, metadata Metadata) context.Context {
	return context.WithValue(ctx, metadataContextKey{}, metadata)
}

// FromContext returns correlation metadata if middleware has attached it.
func FromContext(ctx context.Context) (Metadata, bool) {
	metadata, ok := ctx.Value(metadataContextKey{}).(Metadata)
	return metadata, ok
}

// Propagate copies context correlation IDs into an outbound HTTP or Connect
// header map. It never copies credentials or arbitrary inbound headers.
func Propagate(ctx context.Context, headers http.Header) bool {
	metadata, ok := FromContext(ctx)
	if !ok || !requestIDPattern.MatchString(metadata.RequestID) ||
		!traceIDPattern.MatchString(metadata.TraceID) {
		return false
	}
	headers.Set(RequestIDHeader, metadata.RequestID)
	headers.Set(TraceIDHeader, metadata.TraceID)
	return true
}

// New derives safe inbound IDs and generates missing/invalid values.
func New(headers http.Header, generator IDGenerator) (Metadata, error) {
	if generator == nil {
		generator = defaultIDGenerator{}
	}
	requestID := headers.Get(RequestIDHeader)
	if !requestIDPattern.MatchString(requestID) {
		generated, err := generator.New()
		if err != nil {
			return Metadata{}, errors.New("request metadata generation failed")
		}
		requestID = generated.String()
	}

	traceID := strings.ToLower(headers.Get(TraceIDHeader))
	if !traceIDPattern.MatchString(traceID) {
		traceID = traceIDFromTraceparent(headers.Get(TraceparentHeader))
	}
	if !traceIDPattern.MatchString(traceID) || traceID == strings.Repeat("0", 32) {
		generated, err := generator.New()
		if err != nil {
			return Metadata{}, errors.New("request metadata generation failed")
		}
		traceID = hex.EncodeToString(generated[:])
	}
	return Metadata{RequestID: requestID, TraceID: traceID}, nil
}

func inboundMetadata(
	ctx context.Context,
	headers http.Header,
	generator IDGenerator,
) (Metadata, error) {
	if metadata, ok := FromContext(ctx); ok &&
		requestIDPattern.MatchString(metadata.RequestID) &&
		traceIDPattern.MatchString(metadata.TraceID) &&
		metadata.TraceID != strings.Repeat("0", 32) {
		return metadata, nil
	}
	return New(headers, generator)
}

func traceIDFromTraceparent(value string) string {
	parts := strings.Split(value, "-")
	if len(parts) != 4 || len(parts[0]) != 2 || len(parts[2]) != 16 || len(parts[3]) != 2 {
		return ""
	}
	traceID := strings.ToLower(parts[1])
	if !traceIDPattern.MatchString(traceID) {
		return ""
	}
	return traceID
}

// Middleware propagates HTTP correlation headers and context.
func Middleware(generator IDGenerator) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			metadata, err := New(request.Header, generator)
			if err != nil {
				http.Error(writer, "internal error", http.StatusInternalServerError)
				return
			}
			writer.Header().Set(RequestIDHeader, metadata.RequestID)
			writer.Header().Set(TraceIDHeader, metadata.TraceID)
			next.ServeHTTP(writer, request.WithContext(WithMetadata(request.Context(), metadata)))
		})
	}
}

// Interceptor propagates correlation state for unary and streaming Connect
// handlers. It does not mutate outbound client calls.
type Interceptor struct {
	Generator IDGenerator
}

func (i Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		if request.Spec().IsClient {
			if !Propagate(ctx, request.Header()) {
				metadata, err := New(request.Header(), i.Generator)
				if err != nil {
					return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
				}
				request.Header().Set(RequestIDHeader, metadata.RequestID)
				request.Header().Set(TraceIDHeader, metadata.TraceID)
			}
			return next(ctx, request)
		}
		metadata, err := inboundMetadata(ctx, request.Header(), i.Generator)
		if err != nil {
			return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
		response, err := next(WithMetadata(ctx, metadata), request)
		// Generic Connect handlers return a typed nil *connect.Response inside
		// the AnyResponse interface on failures. Check the dynamic value before
		// accessing headers so error responses cannot panic.
		if response != nil && !reflect.ValueOf(response).IsNil() {
			response.Header().Set(RequestIDHeader, metadata.RequestID)
			response.Header().Set(TraceIDHeader, metadata.TraceID)
		}
		var connectFailure *connect.Error
		if errors.As(err, &connectFailure) {
			connectFailure.Meta().Set(RequestIDHeader, metadata.RequestID)
			connectFailure.Meta().Set(TraceIDHeader, metadata.TraceID)
		}
		return response, err
	}
}

func (i Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		connection := next(ctx, spec)
		Propagate(ctx, connection.RequestHeader())
		return connection
	}
}

func (i Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, connection connect.StreamingHandlerConn) error {
		metadata, err := inboundMetadata(ctx, connection.RequestHeader(), i.Generator)
		if err != nil {
			return connect.NewError(connect.CodeInternal, errors.New("internal error"))
		}
		connection.ResponseHeader().Set(RequestIDHeader, metadata.RequestID)
		connection.ResponseHeader().Set(TraceIDHeader, metadata.TraceID)
		err = next(WithMetadata(ctx, metadata), connection)
		var connectFailure *connect.Error
		if errors.As(err, &connectFailure) {
			connectFailure.Meta().Set(RequestIDHeader, metadata.RequestID)
			connectFailure.Meta().Set(TraceIDHeader, metadata.TraceID)
		}
		return err
	}
}
