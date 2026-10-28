// SPDX-License-Identifier: Apache-2.0
package service

import (
	"context"
	"errors"
	sessionauth "github.com/delinoio/oss/servers/tairouter/internal/auth"
	observability "github.com/delinoio/oss/servers/tairouter/internal/observability"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/packages/tairouter/core"
	"github.com/delinoio/oss/packages/tairouter/providers"
	"github.com/delinoio/oss/packages/tairouter/store"
	v1 "github.com/delinoio/oss/protos/tairouter/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
	"time"
)

type identityKey struct{}
type bearerKey struct{}
type sessionKey struct{}
type management struct {
	providerExecutor providers.Executor
	store            *store.Store
	auth             *sessionauth.Auth
	telemetry        *observability.Telemetry
	inference        *inferenceAdapter
}

func identity(ctx context.Context) sessionauth.Identity {
	i, _ := ctx.Value(identityKey{}).(sessionauth.Identity)
	return i
}
func (m *management) sessionOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !m.auth.SameOrigin(r) {
			connectErrorHTTP(w, r, connect.CodePermissionDenied, "origin rejected")
			return
		}
		i, e := m.auth.Authenticate(r)
		if e != nil {
			code := connect.CodeUnavailable
			if errors.Is(e, sessionauth.ErrAuthentication) || errors.Is(e, store.ErrNotFound) || errors.Is(e, store.ErrExpired) || errors.Is(e, store.ErrForbidden) {
				code = connect.CodeUnauthenticated
			}
			connectErrorHTTP(w, r, code, "session unavailable")
			return
		}
		ctx := context.WithValue(r.Context(), identityKey{}, i)
		if cookie, e := r.Cookie(m.auth.CookieName()); e == nil {
			ctx = context.WithValue(ctx, sessionKey{}, cookie.Value)
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (m *management) clientOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := bearer(r)
		if token == "" {
			connectErrorHTTP(w, r, connect.CodeUnauthenticated, "inference key required")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), bearerKey{}, token)))
	})
}
func bearer(r *http.Request) string {
	v := r.Header.Get("Authorization")
	if !strings.HasPrefix(v, "Bearer ") {
		return ""
	}
	return strings.TrimSpace(strings.TrimPrefix(v, "Bearer "))
}
func connectErrorHTTP(w http.ResponseWriter, r *http.Request, code connect.Code, message string) {
	e := connect.NewError(code, errors.New(message))
	reason := v1.ErrorReason_ERROR_REASON_UNSPECIFIED
	switch code {
	case connect.CodePermissionDenied:
		reason = v1.ErrorReason_ERROR_REASON_PERMISSION_DENIED
	case connect.CodeUnauthenticated:
		reason = v1.ErrorReason_ERROR_REASON_AUTHENTICATION_REQUIRED
	case connect.CodeUnavailable:
		reason = v1.ErrorReason_ERROR_REASON_DATABASE_UNAVAILABLE
	}
	if detail, err := connect.NewErrorDetail(&v1.ErrorDetail{Reason: reason, RequestId: observability.RequestID(r.Context())}); err == nil {
		e.AddDetail(detail)
	}
	e.Meta().Set("X-Request-ID", observability.RequestID(r.Context()))
	_ = connect.NewErrorWriter().Write(w, r, e)
}
func rpcError(err error) error {
	if err == nil {
		return nil
	}
	var existing *connect.Error
	if errors.As(err, &existing) {
		return err
	}
	code := connect.CodeUnavailable
	message := "storage unavailable"
	switch {
	case errors.Is(err, store.ErrForbidden):
		code = connect.CodePermissionDenied
		message = "permission denied"
	case errors.Is(err, store.ErrNotFound):
		code = connect.CodeNotFound
		message = "resource not found"
	case errors.Is(err, store.ErrExpired), errors.Is(err, store.ErrConflict):
		code = connect.CodeFailedPrecondition
		message = "lifecycle conflict"
	case errors.Is(err, context.Canceled):
		code = connect.CodeCanceled
		message = "request canceled"
	case errors.Is(err, sessionauth.ErrAuthentication):
		code = connect.CodeUnauthenticated
		message = "authentication required"
	}
	var limit *store.LimitError
	if errors.As(err, &limit) {
		code = connect.CodeResourceExhausted
		message = "request limit exceeded"
	}
	result := connect.NewError(code, errors.New(message))
	if limit != nil {
		result.Meta().Set("Retry-After", retryAfter(limit.RetryAfter))
	}
	return result
}
func invalid(message string) error {
	return connect.NewError(connect.CodeInvalidArgument, errors.New(message))
}
func unimplemented() error {
	return connect.NewError(connect.CodeUnimplemented, errors.New("provider operation is not supported"))
}
func (m *management) scope(ctx context.Context, org string) (core.Scope, error) {
	if org == "" {
		return core.Scope{}, invalid("organization is required")
	}
	sc, _, e := m.store.Membership(ctx, org, identity(ctx).UserID)
	return sc, rpcError(e)
}
func timestamp(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}
func expiry(t *timestamppb.Timestamp) (*time.Time, error) {
	if t == nil {
		return nil, nil
	}
	if e := t.CheckValid(); e != nil {
		return nil, invalid("invalid expiry")
	}
	v := t.AsTime()
	if !v.After(time.Now()) {
		return nil, invalid("expiry must be in the future")
	}
	return &v, nil
}
func role(r core.Role) v1.Role {
	switch r {
	case core.Owner:
		return v1.Role_ROLE_OWNER
	case core.Administrator:
		return v1.Role_ROLE_ADMINISTRATOR
	default:
		return v1.Role_ROLE_MEMBER
	}
}
