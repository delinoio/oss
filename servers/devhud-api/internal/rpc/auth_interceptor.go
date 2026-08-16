package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

type AuthInterceptor struct {
	verifier   auth.Verifier
	repository domain.Repository
	logger     *slog.Logger
}

func NewAuthInterceptor(verifier auth.Verifier, repository domain.Repository, logger *slog.Logger) *AuthInterceptor {
	return &AuthInterceptor{verifier: verifier, repository: repository, logger: logger}
}

func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		if request.Spec().Procedure == devhudv1connect.BootstrapServiceGetBootstrapProcedure {
			return next(ctx, request)
		}
		identity, err := i.verifier.Verify(ctx, request.Header().Get("Authorization"))
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				return nil, unauthenticatedError(ctx)
			}
			if errors.Is(err, context.Canceled) && !errors.Is(err, auth.ErrVerificationUnavailable) {
				return nil, NewError(connect.CodeCanceled, "request canceled", CorrelationID(ctx))
			}
			if errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, auth.ErrVerificationUnavailable) {
				return nil, NewError(connect.CodeDeadlineExceeded, "request deadline exceeded", CorrelationID(ctx))
			}
			i.logger.ErrorContext(ctx, "identity verification failed",
				"correlation_id", CorrelationID(ctx),
				"procedure", request.Spec().Procedure,
				"error", err,
			)
			return nil, NewError(connect.CodeUnavailable, "identity provider unavailable", CorrelationID(ctx))
		}
		user, err := i.repository.ProvisionUser(ctx, identity)
		if err != nil {
			if errors.Is(err, domain.ErrIdentityPurged) {
				if isAccountProcedure(request.Spec().Procedure) {
					return nil, accountPurgeCompleteError(ctx)
				}
				return nil, deletionCompletePermissionError(ctx)
			}
			i.logger.ErrorContext(ctx, "account provisioning failed",
				"correlation_id", CorrelationID(ctx),
				"procedure", request.Spec().Procedure,
				"error", err,
			)
			return nil, internalError(ctx)
		}
		return next(auth.WithUser(ctx, user), request)
	}
}

func isAccountProcedure(procedure string) bool {
	switch procedure {
	case devhudv1connect.AccountServiceGetAccountProcedure,
		devhudv1connect.AccountServiceDeleteAccountProcedure,
		devhudv1connect.AccountServiceRestoreAccountProcedure:
		return true
	default:
		return false
	}
}

func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
