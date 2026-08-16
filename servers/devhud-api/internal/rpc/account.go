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
	"google.golang.org/protobuf/types/known/timestamppb"
)

type AccountService struct {
	repository domain.Repository
	clock      domain.Clock
	logger     *slog.Logger
}

func NewAccountService(repository domain.Repository, clock domain.Clock, logger *slog.Logger) *AccountService {
	return &AccountService{repository: repository, clock: clock, logger: logger}
}

func (s *AccountService) GetAccount(ctx context.Context, _ *connect.Request[devhudv1.GetAccountRequest]) (*connect.Response[devhudv1.GetAccountResponse], error) {
	user, err := s.currentUser(ctx)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&devhudv1.GetAccountResponse{Metadata: metadata(CorrelationID(ctx)), Account: accountMessage(user)}), nil
}

func (s *AccountService) DeleteAccount(ctx context.Context, _ *connect.Request[devhudv1.DeleteAccountRequest]) (*connect.Response[devhudv1.DeleteAccountResponse], error) {
	principal, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	user, err := s.repository.DeleteAccount(ctx, principal.ID, s.clock.Now())
	if err != nil {
		return nil, s.mapAccountError(ctx, devhudv1connect.AccountServiceDeleteAccountProcedure, err)
	}
	return connect.NewResponse(&devhudv1.DeleteAccountResponse{Metadata: metadata(CorrelationID(ctx)), Account: accountMessage(user)}), nil
}

func (s *AccountService) RestoreAccount(ctx context.Context, _ *connect.Request[devhudv1.RestoreAccountRequest]) (*connect.Response[devhudv1.RestoreAccountResponse], error) {
	principal, ok := auth.UserFromContext(ctx)
	if !ok {
		return nil, unauthenticatedError(ctx)
	}
	user, err := s.repository.RestoreAccount(ctx, principal.ID, s.clock.Now())
	if err != nil {
		return nil, s.mapAccountError(ctx, devhudv1connect.AccountServiceRestoreAccountProcedure, err)
	}
	return connect.NewResponse(&devhudv1.RestoreAccountResponse{Metadata: metadata(CorrelationID(ctx)), Account: accountMessage(user)}), nil
}

func (s *AccountService) currentUser(ctx context.Context) (domain.User, error) {
	principal, ok := auth.UserFromContext(ctx)
	if !ok {
		return domain.User{}, unauthenticatedError(ctx)
	}
	user, err := s.repository.GetAccount(ctx, principal.ID)
	if err != nil {
		return domain.User{}, s.mapAccountError(ctx, devhudv1connect.AccountServiceGetAccountProcedure, err)
	}
	return user, nil
}

func (s *AccountService) mapAccountError(ctx context.Context, procedure string, err error) error {
	var state *domain.AccountStateError
	if errors.As(err, &state) {
		reason := devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_UNSPECIFIED
		if state.Failure == domain.AccountFailureRecoveryExpired {
			reason = devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_RECOVERY_WINDOW_EXPIRED
		} else if state.Failure == domain.AccountFailurePurgeClaimed {
			reason = devhudv1.AccountFailureReason_ACCOUNT_FAILURE_REASON_PURGE_CLAIMED
		}
		return NewError(connect.CodeFailedPrecondition, "account cannot be restored", CorrelationID(ctx), &devhudv1.AccountFailure{Reason: reason})
	}
	if errors.Is(err, domain.ErrIdentityPurged) || errors.Is(err, domain.ErrNotFound) {
		return accountPurgeCompleteError(ctx)
	}
	s.logger.ErrorContext(ctx, "account repository operation failed",
		"correlation_id", CorrelationID(ctx),
		"procedure", procedure,
		"error", err,
	)
	return internalError(ctx)
}

func accountMessage(user domain.User) *devhudv1.Account {
	account := &devhudv1.Account{
		UserId:                   uuid(user.ID),
		LogtoSubject:             user.Subject,
		DisplayName:              user.DisplayName,
		Email:                    user.Email,
		DeletionState:            deletionState(user.DeletionState),
		AdministrativeBlockState: administrativeBlockState(user.AdministrativeBlockState),
		CreatedAt:                timestamppb.New(user.CreatedAt),
	}
	if user.DeletionRequestedAt != nil {
		account.DeletionRequestedAt = timestamppb.New(*user.DeletionRequestedAt)
	}
	if user.RecoverableUntil != nil {
		account.RecoverableUntil = timestamppb.New(*user.RecoverableUntil)
	}
	return account
}

func deletionState(value domain.DeletionState) devhudv1.AccountDeletionState {
	switch value {
	case domain.DeletionStateActive:
		return devhudv1.AccountDeletionState_ACCOUNT_DELETION_STATE_ACTIVE
	case domain.DeletionStatePending:
		return devhudv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PENDING
	case domain.DeletionStatePurgeClaimed:
		return devhudv1.AccountDeletionState_ACCOUNT_DELETION_STATE_PURGE_CLAIMED
	default:
		return devhudv1.AccountDeletionState_ACCOUNT_DELETION_STATE_UNSPECIFIED
	}
}

func administrativeBlockState(value domain.AdministrativeBlockState) devhudv1.AdministrativeBlockState {
	if value == domain.AdministrativeBlockStateBlocked {
		return devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_BLOCKED
	}
	if value == domain.AdministrativeBlockStateUnblocked {
		return devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNBLOCKED
	}
	return devhudv1.AdministrativeBlockState_ADMINISTRATIVE_BLOCK_STATE_UNSPECIFIED
}
