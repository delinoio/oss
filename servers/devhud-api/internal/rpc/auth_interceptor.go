package rpc

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/protos/gen/go/devhud/v1/devhudv1connect"
	"github.com/delinoio/oss/servers/devhud-api/internal/auth"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

type AuthInterceptor struct {
	verifier   auth.Verifier
	repository domain.Repository
}

func NewAuthInterceptor(verifier auth.Verifier, repository domain.Repository) *AuthInterceptor {
	return &AuthInterceptor{verifier: verifier, repository: repository}
}

func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		if request.Spec().Procedure == devhudv1connect.BootstrapServiceGetBootstrapProcedure {
			return next(ctx, request)
		}
		identity, err := i.verifier.Verify(ctx, request.Header().Get("Authorization"))
		if err != nil {
			return nil, unauthenticatedError(ctx)
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
