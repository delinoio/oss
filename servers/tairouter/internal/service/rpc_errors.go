// SPDX-License-Identifier: Apache-2.0
package service

import (
	"connectrpc.com/connect"
	"context"
	"errors"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"math"
	"strconv"
	"time"
)

func errorInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			response, e := next(ctx, req)
			if e == nil {
				if response != nil {
					response.Header().Set("X-Request-ID", observability.RequestID(ctx))
				}
				return response, nil
			}
			var ce *connect.Error
			if !errors.As(e, &ce) {
				ce = connect.NewError(connect.CodeInternal, errors.New("internal service error"))
			}
			reason := v1.ErrorReason_ERROR_REASON_UNSPECIFIED
			switch ce.Code() {
			case connect.CodeInvalidArgument:
				reason = v1.ErrorReason_ERROR_REASON_INVALID_ARGUMENT
			case connect.CodeUnauthenticated:
				reason = v1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED
			case connect.CodePermissionDenied:
				reason = v1.ErrorReason_ERROR_REASON_PERMISSION_DENIED
			case connect.CodeNotFound:
				reason = v1.ErrorReason_ERROR_REASON_RESOURCE_NOT_FOUND
			case connect.CodeFailedPrecondition:
				reason = v1.ErrorReason_ERROR_REASON_CONFLICT
			case connect.CodeCanceled:
				reason = v1.ErrorReason_ERROR_REASON_CANCELED
			case connect.CodeResourceExhausted:
				reason = v1.ErrorReason_ERROR_REASON_RATE_LIMITED
			case connect.CodeUnavailable:
				reason = v1.ErrorReason_ERROR_REASON_DATABASE_UNAVAILABLE
			}
			for _, existing := range ce.Details() {
				if value, err := existing.Value(); err == nil {
					if typed, ok := value.(*v1.ErrorDetail); ok {
						reason = typed.Reason
					}
				}
			}
			detail := &v1.ErrorDetail{Reason: reason, RequestId: observability.RequestID(ctx)}
			if value, e := strconv.ParseUint(ce.Meta().Get("Retry-After"), 10, 32); e == nil {
				v := uint32(value)
				detail.RetryAfterSeconds = &v
			}
			if d, e := connect.NewErrorDetail(detail); e == nil {
				ce.AddDetail(d)
			}
			ce.Meta().Set("X-Request-ID", observability.RequestID(ctx))
			return nil, ce
		}
	}
}
func retryAfter(d time.Duration) string {
	return strconv.Itoa(int(math.Max(1, math.Ceil(d.Seconds()))))
}
