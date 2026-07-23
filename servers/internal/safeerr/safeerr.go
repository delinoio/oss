// Package safeerr maps internal failures to stable, credential-free transport
// errors and classifications.
package safeerr

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/internal/auth"
)

// Class is safe to log and return to callers.
type Class uint8

const (
	ClassInternal Class = iota
	ClassAuthentication
	ClassAuthorization
	ClassInvalidArgument
	ClassNotFound
	ClassConflict
	ClassRateLimited
	ClassDependency
	ClassTimeout
	ClassCanceled
)

func (c Class) String() string {
	switch c {
	case ClassAuthentication:
		return "authentication"
	case ClassAuthorization:
		return "authorization"
	case ClassInvalidArgument:
		return "invalid_argument"
	case ClassNotFound:
		return "not_found"
	case ClassConflict:
		return "conflict"
	case ClassRateLimited:
		return "rate_limited"
	case ClassDependency:
		return "dependency"
	case ClassTimeout:
		return "timeout"
	case ClassCanceled:
		return "canceled"
	default:
		return "internal"
	}
}

// Error contains only a stable class. Internal causes, credentials, billing
// PII, and arbitrary caller messages are intentionally not retained.
type Error struct {
	Class Class
}

func (e *Error) Error() string {
	if e == nil {
		return messageFor(ClassInternal)
	}
	return messageFor(e.Class)
}

// New creates a transport-safe classified error.
func New(class Class) error {
	return &Error{Class: class}
}

// Classify derives a stable class without exposing the source error.
func Classify(err error) Class {
	if err == nil {
		return ClassInternal
	}
	var authFailure *auth.Error
	if errors.As(err, &authFailure) {
		return ClassAuthentication
	}
	var safe *Error
	if errors.As(err, &safe) {
		return safe.Class
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return ClassTimeout
	}
	if errors.Is(err, context.Canceled) {
		return ClassCanceled
	}
	var connectFailure *connect.Error
	if errors.As(err, &connectFailure) {
		return classForConnectCode(connectFailure.Code())
	}
	return ClassInternal
}

func messageFor(class Class) string {
	switch class {
	case ClassAuthentication:
		return "authentication required"
	case ClassAuthorization:
		return "permission denied"
	case ClassInvalidArgument:
		return "invalid request"
	case ClassNotFound:
		return "resource not found"
	case ClassConflict:
		return "request conflict"
	case ClassRateLimited:
		return "too many requests"
	case ClassDependency:
		return "service unavailable"
	case ClassTimeout:
		return "request timed out"
	case ClassCanceled:
		return "request canceled"
	default:
		return "internal error"
	}
}

func httpStatus(class Class) int {
	switch class {
	case ClassAuthentication:
		return http.StatusUnauthorized
	case ClassAuthorization:
		return http.StatusForbidden
	case ClassInvalidArgument:
		return http.StatusBadRequest
	case ClassNotFound:
		return http.StatusNotFound
	case ClassConflict:
		return http.StatusConflict
	case ClassRateLimited:
		return http.StatusTooManyRequests
	case ClassDependency:
		return http.StatusServiceUnavailable
	case ClassTimeout:
		return http.StatusGatewayTimeout
	case ClassCanceled:
		return 499
	default:
		return http.StatusInternalServerError
	}
}

// WriteHTTP emits a stable JSON envelope with no source error text.
func WriteHTTP(writer http.ResponseWriter, err error) {
	class := Classify(err)
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(httpStatus(class))
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]string{
			"class":   class.String(),
			"message": messageFor(class),
		},
	})
}

// HTTPHandler is an error-returning HTTP handler.
type HTTPHandler func(http.ResponseWriter, *http.Request) error

// HTTP maps returned failures and recovered panics to safe responses.
func HTTP(handler HTTPHandler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		defer func() {
			if recover() != nil {
				WriteHTTP(writer, New(ClassInternal))
			}
		}()
		if err := handler(writer, request); err != nil {
			WriteHTTP(writer, err)
		}
	})
}

// Connect maps an arbitrary server error to a safe Connect error. Intentional
// Connect errors retain their code and vetted machine-readable reason while
// source messages, metadata, free-form detail fields, and unrecognized details
// are discarded.
func Connect(err error) error {
	if err == nil {
		return nil
	}
	var connectFailure *connect.Error
	if errors.As(err, &connectFailure) {
		class := classForConnectCode(connectFailure.Code())
		mapped := connect.NewError(connectFailure.Code(), errors.New(messageFor(class)))
		for _, detail := range connectFailure.Details() {
			value, detailErr := detail.Value()
			if detailErr != nil {
				continue
			}
			source, ok := value.(*delibasev1.ErrorDetail)
			if !ok || source.Reason == delibasev1.ErrorReason_ERROR_REASON_UNSPECIFIED {
				continue
			}
			if _, known := delibasev1.ErrorReason_name[int32(source.Reason)]; !known {
				continue
			}
			safe, detailErr := connect.NewErrorDetail(&delibasev1.ErrorDetail{Reason: source.Reason})
			if detailErr == nil {
				mapped.AddDetail(safe)
			}
		}
		return mapped
	}
	class := Classify(err)
	return connect.NewError(connectCode(class), errors.New(messageFor(class)))
}

func connectCode(class Class) connect.Code {
	switch class {
	case ClassAuthentication:
		return connect.CodeUnauthenticated
	case ClassAuthorization:
		return connect.CodePermissionDenied
	case ClassInvalidArgument:
		return connect.CodeInvalidArgument
	case ClassNotFound:
		return connect.CodeNotFound
	case ClassConflict:
		return connect.CodeAborted
	case ClassRateLimited:
		return connect.CodeResourceExhausted
	case ClassDependency:
		return connect.CodeUnavailable
	case ClassTimeout:
		return connect.CodeDeadlineExceeded
	case ClassCanceled:
		return connect.CodeCanceled
	default:
		return connect.CodeInternal
	}
}

func classForConnectCode(code connect.Code) Class {
	switch code {
	case connect.CodeUnauthenticated:
		return ClassAuthentication
	case connect.CodePermissionDenied:
		return ClassAuthorization
	case connect.CodeInvalidArgument, connect.CodeOutOfRange, connect.CodeFailedPrecondition:
		return ClassInvalidArgument
	case connect.CodeNotFound:
		return ClassNotFound
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return ClassConflict
	case connect.CodeResourceExhausted:
		return ClassRateLimited
	case connect.CodeUnavailable:
		return ClassDependency
	case connect.CodeDeadlineExceeded:
		return ClassTimeout
	case connect.CodeCanceled:
		return ClassCanceled
	default:
		return ClassInternal
	}
}

// Interceptor applies safe error mapping to unary and streaming handlers.
type Interceptor struct{}

func (Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (response connect.AnyResponse, err error) {
		defer func() {
			if recover() != nil {
				response = nil
				err = Connect(New(ClassInternal))
			}
		}()
		response, err = next(ctx, request)
		return response, Connect(err)
	}
}

func (Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, connection connect.StreamingHandlerConn) (err error) {
		defer func() {
			if recover() != nil {
				err = Connect(New(ClassInternal))
			}
		}()
		return Connect(next(ctx, connection))
	}
}
