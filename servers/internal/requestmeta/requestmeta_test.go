package requestmeta

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"
)

type fixedGenerator struct {
	ids []uuid.UUID
}

func (g *fixedGenerator) New() (uuid.UUID, error) {
	id := g.ids[0]
	g.ids = g.ids[1:]
	return id, nil
}

type testStreamingHandlerConn struct {
	requestHeader   http.Header
	responseHeader  http.Header
	responseTrailer http.Header
}

func (c *testStreamingHandlerConn) Spec() connect.Spec {
	return connect.Spec{Procedure: "/delibase.v1.UsageService/ReserveUsage"}
}

func (c *testStreamingHandlerConn) Peer() connect.Peer { return connect.Peer{} }
func (c *testStreamingHandlerConn) Receive(any) error  { return nil }
func (c *testStreamingHandlerConn) Send(any) error     { return nil }
func (c *testStreamingHandlerConn) RequestHeader() http.Header {
	return c.requestHeader
}
func (c *testStreamingHandlerConn) ResponseHeader() http.Header {
	return c.responseHeader
}
func (c *testStreamingHandlerConn) ResponseTrailer() http.Header {
	return c.responseTrailer
}

func TestHTTPPropagatesSafeRequestAndTraceIDs(t *testing.T) {
	t.Parallel()
	handler := Middleware(nil)(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		metadata, ok := FromContext(request.Context())
		if !ok {
			t.Fatal("request metadata missing from context")
		}
		if metadata.RequestID != "caller-request-1" ||
			metadata.TraceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
			t.Fatalf("metadata = %#v", metadata)
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set(RequestIDHeader, "caller-request-1")
	request.Header.Set(TraceparentHeader, "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)

	if response.Header().Get(RequestIDHeader) != "caller-request-1" {
		t.Fatalf("response request ID = %q", response.Header().Get(RequestIDHeader))
	}
	if response.Header().Get(TraceIDHeader) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("response trace ID = %q", response.Header().Get(TraceIDHeader))
	}
}

func TestInvalidIDsAreReplaced(t *testing.T) {
	t.Parallel()
	generator := &fixedGenerator{ids: []uuid.UUID{
		uuid.MustParse("018f8a7d-4b1c-7abc-8def-000000000001"),
		uuid.MustParse("018f8a7d-4b1c-7abc-8def-000000000002"),
	}}
	headers := make(http.Header)
	headers.Set(RequestIDHeader, "unsafe\nrequest")
	headers.Set(TraceIDHeader, "not-a-trace")
	metadata, err := New(headers, generator)
	if err != nil {
		t.Fatal(err)
	}
	if metadata.RequestID != "018f8a7d-4b1c-7abc-8def-000000000001" {
		t.Fatalf("generated request ID = %q", metadata.RequestID)
	}
	if metadata.TraceID != "018f8a7d4b1c7abc8def000000000002" {
		t.Fatalf("generated trace ID = %q", metadata.TraceID)
	}
}

func TestPropagateCopiesOnlyCorrelationContext(t *testing.T) {
	t.Parallel()
	ctx := WithMetadata(context.Background(), Metadata{
		RequestID: "outbound-request",
		TraceID:   "4bf92f3577b34da6a3ce929d0e0e4736",
	})
	headers := make(http.Header)
	if !Propagate(ctx, headers) {
		t.Fatal("Propagate() rejected safe metadata")
	}
	if headers.Get(RequestIDHeader) != "outbound-request" ||
		headers.Get(TraceIDHeader) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("propagated headers = %#v", headers)
	}
}

func TestConnectPropagatesRequestID(t *testing.T) {
	t.Parallel()
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set(RequestIDHeader, "connect-request-1")
	request.Header().Set(TraceIDHeader, "4bf92f3577b34da6a3ce929d0e0e4736")
	next := func(ctx context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		metadata, ok := FromContext(ctx)
		if !ok || metadata.RequestID != "connect-request-1" {
			t.Fatalf("metadata = %#v, %v", metadata, ok)
		}
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	response, err := (Interceptor{}).WrapUnary(next)(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if response.Header().Get(RequestIDHeader) != "connect-request-1" {
		t.Fatalf("response request ID = %q", response.Header().Get(RequestIDHeader))
	}
}

func TestConnectPropagatesRequestIDOnError(t *testing.T) {
	t.Parallel()
	request := connect.NewRequest(&emptypb.Empty{})
	request.Header().Set(RequestIDHeader, "connect-error-request")
	request.Header().Set(TraceIDHeader, "4bf92f3577b34da6a3ce929d0e0e4736")
	next := func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	_, err := (Interceptor{}).WrapUnary(next)(context.Background(), request)
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) {
		t.Fatalf("error = %T", err)
	}
	if connectFailure.Meta().Get(RequestIDHeader) != "connect-error-request" {
		t.Fatalf("error request ID = %q", connectFailure.Meta().Get(RequestIDHeader))
	}
}

func TestConnectStreamingPropagatesRequestIDOnError(t *testing.T) {
	t.Parallel()
	connection := &testStreamingHandlerConn{
		requestHeader: http.Header{
			RequestIDHeader: {"connect-stream-error-request"},
			TraceIDHeader:   {"4bf92f3577b34da6a3ce929d0e0e4736"},
		},
		responseHeader:  make(http.Header),
		responseTrailer: make(http.Header),
	}
	next := func(context.Context, connect.StreamingHandlerConn) error {
		return connect.NewError(connect.CodeInternal, errors.New("internal error"))
	}
	err := (Interceptor{}).WrapStreamingHandler(next)(context.Background(), connection)
	var connectFailure *connect.Error
	if !errors.As(err, &connectFailure) {
		t.Fatalf("error = %T", err)
	}
	if connectFailure.Meta().Get(RequestIDHeader) != "connect-stream-error-request" {
		t.Fatalf("error request ID = %q", connectFailure.Meta().Get(RequestIDHeader))
	}
	if connectFailure.Meta().Get(TraceIDHeader) != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("error trace ID = %q", connectFailure.Meta().Get(TraceIDHeader))
	}
}
