package rpc

import (
	"context"
	"errors"
	"log/slog"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
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
				if request.Spec().Procedure == devhudv1connect.AccountServiceRestoreAccountProcedure {
					return nil, NewError(connect.CodeFailedPrecondition, "account purge has completed", CorrelationID(ctx), &devhudv1.AccountFailure{
						Reason: devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_PURGE_CLAIMED,
					})
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

func (i *AuthInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *AuthInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
