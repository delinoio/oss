package rpc

import (
	"context"

	"connectrpc.com/connect"
	devhudv1 "github.com/delinoio/oss/protos/gen/go/devhud/v1"
	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
)

func unauthenticatedError(ctx context.Context) error {
	return NewError(connect.CodeUnauthenticated, "valid Logto credentials are required", CorrelationID(ctx))
}

func adminRolePermissionError(ctx context.Context) error {
	return NewError(connect.CodePermissionDenied, "administrator role is required", CorrelationID(ctx), &devhudv1.PermissionFailure{
		Reason: devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ADMIN_ROLE_REQUIRED,
	})
}

func internalError(ctx context.Context) error {
	return NewError(connect.CodeInternal, "internal service error", CorrelationID(ctx))
}

func deletionCompletePermissionError(ctx context.Context) error {
	return NewError(connect.CodePermissionDenied, "account deletion is complete", CorrelationID(ctx), &devhudv1.PermissionFailure{
		Reason: devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ACCOUNT_DELETION_PENDING,
	})
}

func accountPurgeCompleteError(ctx context.Context) error {
	return NewError(connect.CodeFailedPrecondition, "account purge has completed", CorrelationID(ctx), &devhudv1.AccountFailure{
		Reason: devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_PURGE_CLAIMED,
	})
}

func permissionError(ctx context.Context, permission *domain.PermissionError) error {
	reason := devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_UNSPECIFIED
	message := "account is blocked"
	if permission.Failure == domain.PermissionFailureAdministrativeBlock {
		reason = devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_USER_BLOCKED
	} else if permission.Failure == domain.PermissionFailureDeletionPending {
		reason = devhudv1.PermissionFailureReason_PERMISSION_FAILURE_REASON_ACCOUNT_DELETION_PENDING
		message = "account deletion is pending"
	}
	return NewError(connect.CodePermissionDenied, message, CorrelationID(ctx), &devhudv1.PermissionFailure{Reason: reason})
}
