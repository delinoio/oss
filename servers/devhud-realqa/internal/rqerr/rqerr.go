// Package rqerr maps RealQA failures to stable typed Connect errors.
package rqerr

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/internal/auth"
)

func New(
	code connect.Code,
	reason realqav1.ErrorReason,
	failure realqav1.FailureClass,
	currentRevision int64,
) error {
	mapped := connect.NewError(code, errors.New(message(code)))
	detail := &realqav1.ErrorDetail{Reason: reason, FailureClass: failure}
	if currentRevision > 0 {
		detail.CurrentRevision = Revision(currentRevision)
	}
	typed, err := connect.NewErrorDetail(detail)
	if err == nil {
		mapped.AddDetail(typed)
	}
	return mapped
}

func Revision(value int64) *realqav1.Revision {
	if value <= 0 {
		return nil
	}
	return &realqav1.Revision{Value: value, Etag: ETag(value)}
}

func ETag(revision int64) string {
	if revision <= 0 {
		return ""
	}
	return `"realqa-r` + decimal(revision) + `"`
}

func decimal(value int64) string {
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = byte('0' + value%10)
		value /= 10
	}
	return string(buffer[index:])
}

func authentication(err error, forwarded bool) error {
	var failure *auth.Error
	if errors.As(err, &failure) && failure != nil &&
		failure.Kind == auth.ErrorKeyUnavailable {
		return New(connect.CodeUnavailable, realqav1.ErrorReason_ERROR_REASON_REAUTHENTICATION_REQUIRED,
			realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
	}
	reason := realqav1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED
	class := realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED
	if forwarded {
		reason = realqav1.ErrorReason_ERROR_REASON_REAUTHENTICATION_REQUIRED
		class = realqav1.FailureClass_FAILURE_CLASS_REAUTHENTICATION_REQUIRED
	}
	return New(connect.CodeUnauthenticated, reason, class, 0)
}

// Sanitize discards arbitrary source messages, metadata, and unrecognized
// details while preserving only the closed RealQA error reason/failure/revision.
func Sanitize(err error) error {
	if err == nil {
		return nil
	}
	var authFailure *auth.Error
	if errors.As(err, &authFailure) {
		return authentication(err, authFailure.Credential == auth.CredentialForwardedUser)
	}
	var source *connect.Error
	if !errors.As(err, &source) {
		switch {
		case errors.Is(err, context.Canceled):
			return New(connect.CodeCanceled, realqav1.ErrorReason_ERROR_REASON_UNSPECIFIED,
				realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
		case errors.Is(err, context.DeadlineExceeded):
			return New(connect.CodeDeadlineExceeded, realqav1.ErrorReason_ERROR_REASON_UNSPECIFIED,
				realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
		default:
			return New(connect.CodeInternal, realqav1.ErrorReason_ERROR_REASON_UNSPECIFIED,
				realqav1.FailureClass_FAILURE_CLASS_RETRYABLE, 0)
		}
	}
	mapped := connect.NewError(source.Code(), errors.New(message(source.Code())))
	for _, item := range source.Details() {
		value, detailErr := item.Value()
		if detailErr != nil {
			continue
		}
		detail, ok := value.(*realqav1.ErrorDetail)
		if !ok || detail.Reason == realqav1.ErrorReason_ERROR_REASON_UNSPECIFIED {
			continue
		}
		if _, known := realqav1.ErrorReason_name[int32(detail.Reason)]; !known {
			continue
		}
		safe := &realqav1.ErrorDetail{
			Reason: detail.Reason, FailureClass: detail.FailureClass,
		}
		if detail.CurrentRevision != nil && detail.CurrentRevision.Value > 0 {
			safe.CurrentRevision = Revision(detail.CurrentRevision.Value)
		}
		typed, detailErr := connect.NewErrorDetail(safe)
		if detailErr == nil {
			mapped.AddDetail(typed)
		}
	}
	return mapped
}

func message(code connect.Code) string {
	switch code {
	case connect.CodeUnauthenticated:
		return "authentication required"
	case connect.CodePermissionDenied:
		return "permission denied"
	case connect.CodeInvalidArgument, connect.CodeFailedPrecondition:
		return "invalid request"
	case connect.CodeNotFound:
		return "resource not found"
	case connect.CodeAlreadyExists, connect.CodeAborted:
		return "request conflict"
	case connect.CodeResourceExhausted:
		return "limit exceeded"
	case connect.CodeUnavailable:
		return "service unavailable"
	case connect.CodeDeadlineExceeded:
		return "request timed out"
	case connect.CodeCanceled:
		return "request canceled"
	case connect.CodeUnimplemented:
		return "operation unavailable"
	default:
		return "internal error"
	}
}

type Interceptor struct{}

func (Interceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (
		response connect.AnyResponse, err error,
	) {
		defer func() {
			if recover() != nil {
				response = nil
				err = Sanitize(errors.New("panic"))
			}
		}()
		response, err = next(ctx, request)
		return response, Sanitize(err)
	}
}

func (Interceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (Interceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, connection connect.StreamingHandlerConn) (err error) {
		defer func() {
			if recover() != nil {
				err = Sanitize(errors.New("panic"))
			}
		}()
		return Sanitize(next(ctx, connection))
	}
}
