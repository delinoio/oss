package service

import (
	"context"
	"errors"

	"connectrpc.com/connect"
	delibasev1 "github.com/delinoio/oss/protos/delibase/gen/go/delibase/v1"
	"github.com/delinoio/oss/servers/delibase/internal/database/dbgen"
	"github.com/delinoio/oss/servers/delibase/internal/reliability"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (service *Account) GetAccountState(
	ctx context.Context,
	_ *connect.Request[delibasev1.GetAccountStateRequest],
) (*connect.Response[delibasev1.GetAccountStateResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if service.dependencies.Store == nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	account, err := service.dependencies.Store.Queries().GetAccountByLogtoSubject(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		deleted, deletedErr := service.dependencies.Store.Queries().
			GetDeletedAccountSubject(ctx, subjectDigest(subject))
		if deletedErr == nil {
			return connect.NewResponse(&delibasev1.GetAccountStateResponse{
				Account: &delibasev1.Account{
					AccountId:   uuidMessage(deleted.AccountID),
					Status:      delibasev1.AccountStatus_ACCOUNT_STATUS_DELETED,
					DisplayName: "",
					CreatedAt:   timestamp(deleted.DeletedAt),
					UpdatedAt:   timestamp(deleted.DeletedAt),
				},
				OnboardingRequired: false,
				Organizations:      []*delibasev1.AccountOrganization{},
			}), nil
		}
		if !errors.Is(deletedErr, pgx.ErrNoRows) {
			return nil, databaseError(deletedErr)
		}
		return connect.NewResponse(&delibasev1.GetAccountStateResponse{
			OnboardingRequired: true,
			Organizations:      []*delibasev1.AccountOrganization{},
		}), nil
	}
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.GetAccountStateResponse{
		Account:       accountMessage(account),
		Organizations: []*delibasev1.AccountOrganization{},
	}
	if account.Status != "active" {
		return connect.NewResponse(response), nil
	}
	organizations, err := service.dependencies.Store.Queries().
		ListAccountOrganizations(ctx, account.ID)
	if err != nil {
		return nil, databaseError(err)
	}
	response.OnboardingRequired = len(organizations) == 0
	for _, organization := range organizations {
		response.Organizations = append(response.Organizations, &delibasev1.AccountOrganization{
			OrganizationId: uuidMessage(organization.ID),
			Name:           organization.Name,
			Slug:           organization.Slug,
			Role:           organizationRole(organization.Role),
		})
	}
	return connect.NewResponse(response), nil
}

func (service *Account) CompleteOnboarding(
	ctx context.Context,
	request *connect.Request[delibasev1.CompleteOnboardingRequest],
) (*connect.Response[delibasev1.CompleteOnboardingResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil {
		return nil, invalidArgument()
	}
	displayName, err := validateDisplayName(request.Msg.DisplayName)
	if err != nil {
		return nil, err
	}
	organizationName, err := validateName(request.Msg.OrganizationName)
	if err != nil {
		return nil, err
	}
	slug, err := validateSlug(request.Msg.OrganizationSlug)
	if err != nil {
		return nil, err
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	if service.dependencies.Store == nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	accountID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	organizationID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	generalTeamID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	digest := requestDigest(displayName, organizationName, slug)
	var response *delibasev1.CompleteOnboardingResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			response = &delibasev1.CompleteOnboardingResponse{}
			replayed, completedAt, transactionErr := replay(
				ctx,
				queries,
				subject,
				"complete_onboarding",
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMPLETE_ONBOARDING,
					true,
					completedAt,
				)
				return nil
			}
			if _, transactionErr = queries.GetDeletedAccountSubject(
				ctx, subjectDigest(subject),
			); transactionErr == nil {
				return serviceError(
					connect.CodePermissionDenied,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_DELETED,
				)
			} else if !errors.Is(transactionErr, pgx.ErrNoRows) {
				return databaseError(transactionErr)
			}
			account, transactionErr := queries.EnsureAccount(ctx, dbgen.EnsureAccountParams{
				ID:           pgUUID(accountID),
				LogtoSubject: subject,
				DisplayName:  displayName,
			})
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			replayed, completedAt, transactionErr = replay(
				ctx,
				queries,
				subject,
				"complete_onboarding",
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMPLETE_ONBOARDING,
					true,
					completedAt,
				)
				return nil
			}
			if account.Status != "active" {
				return serviceError(
					connect.CodePermissionDenied,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_DELETED,
				)
			}
			onboarded, transactionErr := queries.HasAccountOrganization(ctx, account.ID)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if onboarded {
				return serviceError(
					connect.CodeAlreadyExists,
					delibasev1.ErrorReason_ERROR_REASON_RESOURCE_CONFLICT,
				)
			}
			polarCustomerID, transactionErr := ensurePolarCustomer(
				ctx,
				service.dependencies,
				organizationID,
				organizationName,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if account.DisplayName != displayName {
				account, transactionErr = queries.UpdateAccountDisplayName(
					ctx,
					dbgen.UpdateAccountDisplayNameParams{
						DisplayName: displayName,
						ID:          account.ID,
					},
				)
				if transactionErr != nil {
					return databaseError(transactionErr)
				}
			}
			organization, transactionErr := createOrganizationBundle(
				ctx,
				queries,
				account.ID,
				organizationID,
				generalTeamID,
				organizationName,
				slug,
				polarCustomerID,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if transactionErr := appendAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditOrganizationCreated,
				actor,
				uuid.UUID(organization.ID.Bytes),
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response = &delibasev1.CompleteOnboardingResponse{
				Account:        accountMessage(account),
				OrganizationId: uuidMessage(organization.ID),
				GeneralTeamId:  &delibasev1.UuidV7{Value: generalTeamID.String()},
			}
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_COMPLETE_ONBOARDING,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotency(
				ctx,
				service.dependencies,
				queries,
				subject,
				"complete_onboarding",
				key,
				digest,
				response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func (service *Account) GetAccountDeletionImpact(
	ctx context.Context,
	_ *connect.Request[delibasev1.GetAccountDeletionImpactRequest],
) (*connect.Response[delibasev1.GetAccountDeletionImpactResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if service.dependencies.Store == nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	account, err := service.dependencies.Store.Queries().GetAccountByLogtoSubject(ctx, subject)
	if errors.Is(err, pgx.ErrNoRows) {
		if _, deletedErr := service.dependencies.Store.Queries().
			GetDeletedAccountSubject(ctx, subjectDigest(subject)); deletedErr == nil {
			return nil, serviceError(
				connect.CodeFailedPrecondition,
				delibasev1.ErrorReason_ERROR_REASON_DELETION_ALREADY_PENDING,
			)
		} else if !errors.Is(deletedErr, pgx.ErrNoRows) {
			return nil, databaseError(deletedErr)
		}
		return connect.NewResponse(&delibasev1.GetAccountDeletionImpactResponse{
			CanDelete: true,
			Blockers:  []*delibasev1.DeletionBlocker{},
		}), nil
	}
	if err != nil {
		return nil, databaseError(err)
	}
	if account.Status != "active" {
		return nil, serviceError(
			connect.CodeFailedPrecondition,
			delibasev1.ErrorReason_ERROR_REASON_DELETION_ALREADY_PENDING,
		)
	}
	blockers, err := service.dependencies.Store.Queries().
		ListLastOwnerBlockers(ctx, account.ID)
	if err != nil {
		return nil, databaseError(err)
	}
	reservationBlockers, err := service.dependencies.Store.Queries().
		ListActiveReservationBlockersForAccount(ctx, account.ID)
	if err != nil {
		return nil, databaseError(err)
	}
	response := &delibasev1.GetAccountDeletionImpactResponse{
		CanDelete: len(blockers) == 0 && len(reservationBlockers) == 0,
		Blockers:  deletionBlockers(blockers, reservationBlockers),
	}
	return connect.NewResponse(response), nil
}

func (service *Account) DeleteAccount(
	ctx context.Context,
	request *connect.Request[delibasev1.DeleteAccountRequest],
) (*connect.Response[delibasev1.DeleteAccountResponse], error) {
	subject, err := userSubject(ctx)
	if err != nil {
		return nil, err
	}
	if request == nil || request.Msg == nil || !request.Msg.Confirm {
		return nil, invalidArgument()
	}
	key, err := validateIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	if service.dependencies.Store == nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	actor, err := actorFor(service.dependencies, subject)
	if err != nil {
		return nil, err
	}
	deletionID, err := service.dependencies.IDs.New()
	if err != nil {
		return nil, serviceError(connect.CodeInternal, 0)
	}
	digest := requestDigest("confirm")
	var response *delibasev1.DeleteAccountResponse
	err = service.dependencies.Store.WithinTransaction(
		ctx,
		pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			account, transactionErr := queries.LockAccountByLogtoSubject(ctx, subject)
			if errors.Is(transactionErr, pgx.ErrNoRows) {
				response = &delibasev1.DeleteAccountResponse{}
				replayed, completedAt, replayErr := replay(
					ctx,
					queries,
					subject,
					"delete_account",
					key,
					digest,
					response,
				)
				if replayErr != nil {
					return replayErr
				}
				if replayed {
					setIdempotency(
						&response.Idempotency,
						delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_ACCOUNT,
						true,
						completedAt,
					)
					return nil
				}
				if _, deletedErr := queries.GetDeletedAccountSubject(
					ctx, subjectDigest(subject),
				); deletedErr == nil {
					return serviceError(
						connect.CodeFailedPrecondition,
						delibasev1.ErrorReason_ERROR_REASON_DELETION_ALREADY_PENDING,
					)
				} else if !errors.Is(deletedErr, pgx.ErrNoRows) {
					return databaseError(deletedErr)
				}
				return databaseError(transactionErr)
			}
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			response = &delibasev1.DeleteAccountResponse{}
			replayed, completedAt, transactionErr := replay(
				ctx,
				queries,
				subject,
				"delete_account",
				key,
				digest,
				response,
			)
			if transactionErr != nil {
				return transactionErr
			}
			if replayed {
				setIdempotency(
					&response.Idempotency,
					delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_ACCOUNT,
					true,
					completedAt,
				)
				return nil
			}
			if account.Status != "active" {
				return serviceError(
					connect.CodeFailedPrecondition,
					delibasev1.ErrorReason_ERROR_REASON_DELETION_ALREADY_PENDING,
				)
			}
			if _, transactionErr = queries.LockOwnedOrganizations(ctx, account.ID); transactionErr != nil {
				return databaseError(transactionErr)
			}
			blockers, transactionErr := queries.ListLastOwnerBlockers(ctx, account.ID)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			reservationBlockers, transactionErr := queries.
				ListActiveReservationBlockersForAccount(ctx, account.ID)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if len(blockers) > 0 || len(reservationBlockers) > 0 {
				return accountDeletionBlocked(blockers, reservationBlockers)
			}
			if _, transactionErr = queries.DisableAndEraseAccount(ctx, account.ID); transactionErr != nil {
				return databaseError(transactionErr)
			}
			if _, transactionErr = queries.DeleteAccountMemberships(ctx, account.ID); transactionErr != nil {
				return databaseError(transactionErr)
			}
			if _, transactionErr = queries.InsertDeletionTombstone(
				ctx,
				dbgen.InsertDeletionTombstoneParams{
					EntityType:     "account",
					EntityID:       account.ID,
					ActorReference: string(actor),
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
			if _, transactionErr = queries.InsertDeletedAccountSubject(
				ctx,
				dbgen.InsertDeletedAccountSubjectParams{
					SubjectDigest:  subjectDigest(subject),
					AccountID:      account.ID,
					ActorReference: string(actor),
				},
			); transactionErr != nil {
				return databaseError(transactionErr)
			}
			enqueuedID, transactionErr := reliability.EnqueueDeletion(
				ctx,
				queries,
				reliability.DeletionInput{
					ID:             deletionID,
					Type:           reliability.DeletionAccount,
					AccountID:      uuid.UUID(account.ID.Bytes),
					IdempotencyKey: key,
					Actor:          actor,
				},
			)
			if transactionErr != nil {
				return databaseError(transactionErr)
			}
			if transactionErr = appendAudit(
				ctx,
				service.dependencies,
				queries,
				reliability.AuditAccountDeletionRequested,
				actor,
				uuid.Nil,
			); transactionErr != nil {
				return transactionErr
			}
			completedAt = service.dependencies.Clock.Now().UTC()
			response = &delibasev1.DeleteAccountResponse{
				DeletionId: &delibasev1.UuidV7{Value: enqueuedID.String()},
				Status:     delibasev1.DeletionStatus_DELETION_STATUS_EXTERNAL_ACTION_PENDING,
				AcceptedAt: timestamppb.New(completedAt),
			}
			setIdempotency(
				&response.Idempotency,
				delibasev1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_ACCOUNT,
				false,
				completedAt,
			)
			_, transactionErr = persistIdempotency(
				ctx,
				service.dependencies,
				queries,
				subject,
				"delete_account",
				key,
				digest,
				response,
			)
			return transactionErr
		},
	)
	if err != nil {
		return nil, databaseError(err)
	}
	return connect.NewResponse(response), nil
}

func deletionBlockers(
	ownerRows []dbgen.ListLastOwnerBlockersRow,
	reservationRows []dbgen.ListActiveReservationBlockersForAccountRow,
) []*delibasev1.DeletionBlocker {
	blockers := make(
		[]*delibasev1.DeletionBlocker,
		0,
		len(ownerRows)+len(reservationRows),
	)
	for _, row := range ownerRows {
		blockers = append(blockers, &delibasev1.DeletionBlocker{
			Kind:             delibasev1.DeletionBlockerKind_DELETION_BLOCKER_KIND_LAST_ORGANIZATION_OWNER,
			OrganizationId:   uuidMessage(row.ID),
			OrganizationName: row.Name,
		})
	}
	for _, row := range reservationRows {
		blockers = append(blockers, &delibasev1.DeletionBlocker{
			Kind:             delibasev1.DeletionBlockerKind_DELETION_BLOCKER_KIND_ACTIVE_USAGE_RESERVATION,
			OrganizationId:   uuidMessage(row.ID),
			TeamId:           uuidMessage(row.TeamID),
			OrganizationName: row.Name,
			TeamName:         row.TeamName,
		})
	}
	return blockers
}

func accountDeletionBlocked(
	ownerRows []dbgen.ListLastOwnerBlockersRow,
	reservationRows []dbgen.ListActiveReservationBlockersForAccountRow,
) error {
	failure := connect.NewError(connect.CodeFailedPrecondition, errors.New("request failed"))
	detail, err := connect.NewErrorDetail(&delibasev1.ErrorDetail{
		Reason:           delibasev1.ErrorReason_ERROR_REASON_ACCOUNT_DELETION_BLOCKED,
		DeletionBlockers: deletionBlockers(ownerRows, reservationRows),
	})
	if err == nil {
		failure.AddDetail(detail)
	}
	return failure
}

func NewAccountDeletionHandler(
	store interface {
		Queries() dbgen.Querier
		WithinTransaction(
			context.Context,
			pgx.TxOptions,
			func(*dbgen.Queries) error,
		) error
	},
	identity interface {
		DeleteUser(context.Context, string) error
	},
) reliability.Handler {
	return func(ctx context.Context, item reliability.Item) error {
		if store == nil || identity == nil || item.EntityID == uuid.Nil {
			return reliability.ErrInvalidInput
		}
		account, lookupErr := store.Queries().GetAccountByID(ctx, pgUUID(item.EntityID))
		if errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil
		}
		if lookupErr != nil {
			return lookupErr
		}
		if account.Status != "disabled" {
			return reliability.ErrInvalidInput
		}
		if deleteErr := identity.DeleteUser(ctx, account.LogtoSubject); deleteErr != nil {
			return deleteErr
		}
		return store.WithinTransaction(ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			affected, deleteErr := queries.DeleteDisabledAccount(ctx, account.ID)
			if deleteErr != nil {
				return deleteErr
			}
			if affected == 0 {
				return nil
			}
			return nil
		})
	}
}
