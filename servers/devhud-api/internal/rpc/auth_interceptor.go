package rpc

import (
	"context"
	"errors"
	"log/slog"
	"slices"

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
	admin      domain.AdminRepository
	clock      domain.Clock
	ids        domain.IDGenerator
}

func NewAuthInterceptor(verifier auth.Verifier, repository domain.Repository, admin domain.AdminRepository, clock domain.Clock, ids domain.IDGenerator, logger *slog.Logger) *AuthInterceptor {
	return &AuthInterceptor{verifier: verifier, repository: repository, admin: admin, clock: clock, ids: ids, logger: logger}
}

func (i *AuthInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, request connect.AnyRequest) (connect.AnyResponse, error) {
		if request.Spec().Procedure == devhudv1connect.BootstrapServiceGetBootstrapProcedure {
			return next(ctx, request)
		}
		identity, err := i.verifier.Verify(ctx, request.Header().Get("Authorization"))
		if err != nil {
			if errors.Is(err, auth.ErrUnauthenticated) {
				i.recordRejectedAdminMutation(ctx, request, nil, domain.AuditRejectionUnauthenticated)
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
		if isAdminProcedure(request.Spec().Procedure) {
			if !slices.Contains(identity.Roles, domain.AdminRole) {
				i.recordRejectedAdminMutation(ctx, request, &user, domain.AuditRejectionAdminRoleRequired)
				return nil, adminRolePermissionError(ctx)
			}
			if user.AdministrativeBlockState == domain.AdministrativeBlockStateBlocked {
				i.recordRejectedAdminMutation(ctx, request, &user, domain.AuditRejectionActorBlocked)
				return nil, permissionError(ctx, &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock})
			}
			if user.DeletionState != domain.DeletionStateActive {
				i.recordRejectedAdminMutation(ctx, request, &user, domain.AuditRejectionActorBlocked)
				return nil, permissionError(ctx, &domain.PermissionError{Failure: domain.PermissionFailureDeletionPending})
			}
		}
		return next(auth.WithPrincipal(ctx, auth.Principal{User: user, Roles: identity.Roles}), request)
	}
}

func isAdminProcedure(procedure string) bool {
	switch procedure {
	case devhudv1connect.AdminServiceListUsersProcedure,
		devhudv1connect.AdminServiceSetUserBlockedProcedure,
		devhudv1connect.AdminServiceGetUserUsageProcedure,
		devhudv1connect.AdminServiceListUploadsProcedure,
		devhudv1connect.AdminServiceQuarantineUploadProcedure,
		devhudv1connect.AdminServiceDeleteUploadProcedure,
		devhudv1connect.AdminServiceListAuditEventsProcedure:
		return true
	default:
		return false
	}
}

func isAdminMutation(procedure string) bool {
	return procedure == devhudv1connect.AdminServiceSetUserBlockedProcedure ||
		procedure == devhudv1connect.AdminServiceQuarantineUploadProcedure ||
		procedure == devhudv1connect.AdminServiceDeleteUploadProcedure
}

func (i *AuthInterceptor) recordRejectedAdminMutation(ctx context.Context, request connect.AnyRequest, actor *domain.User, reason domain.AuditRejectionReason) {
	if i.admin == nil || !isAdminMutation(request.Spec().Procedure) {
		return
	}
	event, err := i.rejectedAdminMutationEvent(ctx, reason)
	if err != nil {
		return
	}
	if actor != nil {
		event.ActorUserID = &actor.ID
	}
	switch message := request.Any().(type) {
	case *devhudv1.SetUserBlockedRequest:
		if message.GetTargetState() == devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED {
			event.Action = domain.AuditActionUserBlocked
		} else {
			event.Action = domain.AuditActionUserUnblocked
		}
		if value := message.GetUserId().GetValue(); value != "" {
			event.TargetUserID = &value
		}
	case *devhudv1.QuarantineUploadRequest:
		event.Action = domain.AuditActionUploadQuarantined
		if value := message.GetUploadId().GetValue(); value != "" {
			event.TargetUploadID = &value
		}
	case *devhudv1.AdminServiceDeleteUploadRequest:
		event.Action = domain.AuditActionUploadDeleted
		if value := message.GetUploadId().GetValue(); value != "" {
			event.TargetUploadID = &value
		}
	}
	auditContext, cancel := detachedAuditContext(ctx)
	defer cancel()
	if err := i.admin.RecordAdministratorAudit(auditContext, event); err != nil {
		i.logger.WarnContext(ctx, "administrator rejection audit failed", "correlation_id", CorrelationID(ctx), "procedure", request.Spec().Procedure, "error_type", "persistence")
	}
}

func detachedAuditContext(ctx context.Context) (context.Context, context.CancelFunc) {
	detached := context.WithoutCancel(ctx)
	deadline, ok := ctx.Deadline()
	if !ok {
		return detached, func() {}
	}
	return context.WithDeadline(detached, deadline)
}

func (i *AuthInterceptor) rejectedAdminMutationEvent(ctx context.Context, reason domain.AuditRejectionReason) (domain.AuditEvent, error) {
	eventID, err := i.ids.New()
	if err != nil {
		return domain.AuditEvent{}, err
	}
	now := i.clock.Now()
	return domain.AuditEvent{
		ID: eventID, CorrelationID: CorrelationID(ctx), Outcome: domain.AuditOutcomeRejected,
		RejectionReason: reason, CreatedAt: now, ExpiresAt: now.Add(domain.AuditRetention),
	}, nil
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
