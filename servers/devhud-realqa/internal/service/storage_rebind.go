package service

import (
	"bytes"
	"context"
	"errors"
	"time"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/rqerr"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (service *Submission) RebindSubmissionStorageAuthorization(
	ctx context.Context,
	request *connect.Request[realqav1.RebindSubmissionStorageAuthorizationRequest],
) (*connect.Response[realqav1.RebindSubmissionStorageAuthorizationResponse], error) {
	if request == nil || request.Msg == nil ||
		request.Msg.ExpectedMappingRevision == nil ||
		request.Msg.ExpectedMappingRevision.Value <= 0 ||
		request.Msg.ReplacementBilling == nil {
		return nil, invalid(
			realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	idempotencyID, err := parseIdempotency(request.Msg.Idempotency)
	if err != nil {
		return nil, err
	}
	requestDigest, err := digestMessage(request.Msg)
	if err != nil {
		return nil, err
	}
	actor, submissionID, submission, scope, err :=
		service.authorizeSubmissionOwnerRequest(
			ctx, request.Msg.SubmissionId)
	if err != nil {
		return nil, err
	}
	if scope.kind == "organization" {
		if _, err = authorizeOwner(
			ctx, service.dependencies, actor, scope, true, true); err != nil {
			return nil, err
		}
	}
	if submission.State != "storage_billing_grace" {
		return nil, rqerr.New(
			connect.CodeFailedPrecondition,
			realqav1.ErrorReason_ERROR_REASON_STORAGE_REBIND_REQUIRED,
			realqav1.FailureClass_FAILURE_CLASS_USER_ACTION_REQUIRED,
			submission.Revision,
		)
	}
	expectedAuthorizationID, err := parseUUIDMessage(
		request.Msg.ExpectedAuthorizationId)
	if err != nil {
		return nil, storageAuthorizationSubstitution()
	}
	replacementOrganizationID, replacementTeamID, err := authorizeBilling(
		ctx, service.dependencies, actor, scope,
		request.Msg.ReplacementBilling)
	if err != nil {
		return nil, err
	}
	if service.dependencies.Billing == nil {
		return nil, storageAuthorizationFailed()
	}
	forwardedBearer, ok := service.forwardedBearer(ctx)
	if !ok {
		return nil, reauthenticationRequired()
	}
	meters, err := service.dependencies.Billing.Meters(ctx)
	if err != nil || validateBillingMeters(meters) != nil {
		return nil, storageAuthorizationFailed()
	}
	maximumUnits, err := ceilMiB(submission.VerifiedEncodedBytes)
	if err != nil || maximumUnits <= 0 {
		return nil, retentionStateConflict()
	}
	revokeKey, err := derivedUUIDv7(idempotencyID, "storage-rebind-revoke")
	if err != nil {
		return nil, err
	}
	createKey, err := derivedUUIDv7(idempotencyID, "storage-rebind-create")
	if err != nil {
		return nil, err
	}
	attempt, err := service.dependencies.Store.Queries().
		CreateStorageRebindAttempt(
			ctx, dbgen.CreateStorageRebindAttemptParams{
				SubmissionID:              toPGUUID(submissionID),
				CallerDigest:              actor.digest,
				IdempotencyKey:            toPGUUID(idempotencyID),
				RequestDigest:             requestDigest,
				ExpectedAuthorizationID:   toPGUUID(expectedAuthorizationID),
				ExpectedMappingRevision:   request.Msg.ExpectedMappingRevision.Value,
				ReplacementOrganizationID: toPGUUID(replacementOrganizationID),
				ReplacementTeamID:         toPGUUID(replacementTeamID),
				RevokeIdempotencyKey:      toPGUUID(revokeKey),
				CreateIdempotencyKey:      toPGUUID(createKey),
			})
	if errors.Is(err, pgx.ErrNoRows) {
		attempt, err = service.dependencies.Store.Queries().
			GetStorageRebindAttempt(
				ctx, dbgen.GetStorageRebindAttemptParams{
					CallerDigest:   actor.digest,
					IdempotencyKey: toPGUUID(idempotencyID),
				})
	}
	if err != nil {
		return nil, err
	}
	if attempt.SubmissionID != toPGUUID(submissionID) ||
		!bytes.Equal(attempt.RequestDigest, requestDigest) ||
		attempt.ExpectedAuthorizationID !=
			toPGUUID(expectedAuthorizationID) ||
		attempt.ExpectedMappingRevision !=
			request.Msg.ExpectedMappingRevision.Value ||
		attempt.ReplacementOrganizationID !=
			toPGUUID(replacementOrganizationID) ||
		attempt.ReplacementTeamID != toPGUUID(replacementTeamID) ||
		attempt.RevokeIdempotencyKey != toPGUUID(revokeKey) ||
		attempt.CreateIdempotencyKey != toPGUUID(createKey) {
		return nil, idempotencyConflict()
	}
	if attempt.State == "completed" {
		return storageRebindResponse(submissionID, attempt, true)
	}
	binding, err := service.dependencies.Store.Queries().
		GetCurrentStorageAuthorizationBinding(
			ctx, toPGUUID(submissionID))
	if err != nil ||
		binding.AuthorizationID != toPGUUID(expectedAuthorizationID) ||
		binding.MappingRevision !=
			request.Msg.ExpectedMappingRevision.Value {
		return nil, staleStorageAuthorizationMapping()
	}
	recovery, err := service.dependencies.Store.Queries().
		GetActiveStorageRecovery(ctx, toPGUUID(submissionID))
	if err != nil {
		return nil, err
	}
	if recovery.AuthorizationID != binding.AuthorizationID {
		return nil, staleStorageAuthorizationMapping()
	}
	if binding.AccrualCutoffAt.Valid {
		if err = service.prepareStorageRebindCutoff(
			ctx, binding, recovery); err != nil {
			return nil, storageAuthorizationFailed()
		}
	}
	var completed dbgen.RealqaStorageRebindAttempt
	replayed := false
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			lockedAttempt, lockErr := queries.LockStorageRebindAttempt(
				ctx, dbgen.LockStorageRebindAttemptParams{
					CallerDigest:   actor.digest,
					IdempotencyKey: toPGUUID(idempotencyID),
				})
			if lockErr != nil {
				return lockErr
			}
			if lockedAttempt.State == "completed" {
				completed = lockedAttempt
				replayed = true
				return nil
			}
			if lockErr := lockActiveOwnerScope(
				ctx, queries, scope); lockErr != nil {
				return lockErr
			}
			replacementPayer := owner{
				kind: "organization",
				id:   replacementOrganizationID,
			}
			if replacementPayer != scope {
				if lockErr := lockActiveOwnerScope(
					ctx, queries, replacementPayer); lockErr != nil {
					return lockErr
				}
			}
			payerAllowed, lockErr := queries.HasPayerTeamAccess(
				ctx, dbgen.HasPayerTeamAccessParams{
					AccountID:      toPGUUID(actor.accountID),
					OrganizationID: toPGUUID(replacementOrganizationID),
					TeamID:         toPGUUID(replacementTeamID),
				})
			if lockErr != nil {
				return lockErr
			}
			if !payerAllowed {
				return permissionDenied()
			}
			lockedSubmission, lockErr := queries.LockSubmissionRecord(
				ctx, toPGUUID(submissionID))
			if lockErr != nil {
				return lockErr
			}
			if lockedSubmission.State != "storage_billing_grace" {
				return staleStorageAuthorizationMapping()
			}
			lockedMaximumUnits, lockErr := ceilMiB(
				lockedSubmission.VerifiedEncodedBytes)
			if lockErr != nil || lockedMaximumUnits <= 0 {
				return retentionStateConflict()
			}
			lockedBinding, lockErr :=
				queries.LockCurrentStorageAuthorizationBinding(
					ctx, toPGUUID(submissionID))
			if lockErr != nil ||
				lockedBinding.AuthorizationID !=
					toPGUUID(expectedAuthorizationID) ||
				lockedBinding.MappingRevision !=
					request.Msg.ExpectedMappingRevision.Value {
				return staleStorageAuthorizationMapping()
			}
			current, billingErr :=
				service.dependencies.Billing.GetStorageAuthorization(
					ctx, StorageAuthorizationLookupRequest{
						AuthorizationID: expectedAuthorizationID,
						ForwardedBearer: forwardedBearer,
					})
			if billingErr != nil ||
				validateBoundStorageAuthorization(
					current, lockedBinding, false) != nil {
				return storageAuthorizationSubstitution()
			}
			switch current.Status {
			case "active":
				current, billingErr =
					service.dependencies.Billing.RevokeStorageAuthorization(
						ctx, StorageAuthorizationRevokeRequest{
							AuthorizationID:  expectedAuthorizationID,
							ExpectedRevision: current.Revision,
							IdempotencyKey:   revokeKey,
							ForwardedBearer:  forwardedBearer,
						})
				if billingErr != nil ||
					validateBoundStorageAuthorization(
						current, lockedBinding, false) != nil ||
					current.Status != "revoked" {
					return storageAuthorizationFailed()
				}
			case "revoked", "access_lost":
				// The exact already-closed grant is an allowed source.
			default:
				return rqerr.New(
					connect.CodeFailedPrecondition,
					realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_STATUS_INVALID,
					realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
					lockedBinding.AuthorizationRevision,
				)
			}
			replacementRequest := StorageAuthorizationRequest{
				OwnerKind:         scope.kind,
				OwnerID:           scope.id,
				OrganizationID:    replacementOrganizationID,
				TeamID:            replacementTeamID,
				ServiceIdentityID: meters.Storage.ServiceIdentityID,
				MeterID:           meters.Storage.ID,
				FeatureResourceID: submissionID,
				MaximumUnits:      lockedMaximumUnits,
				IdempotencyKey:    createKey,
				ForwardedBearer:   forwardedBearer,
			}
			replacement, billingErr :=
				service.dependencies.Billing.CreateStorageAuthorization(
					ctx, replacementRequest)
			if billingErr != nil || validateStorageAuthorization(
				replacement, replacementRequest, actor.accountID) != nil {
				return storageAuthorizationFailed()
			}
			cutoff := service.dependencies.Clock.Now().UTC()
			if _, lockErr = queries.UpdateStorageAuthorizationStatus(
				ctx, dbgen.UpdateStorageAuthorizationStatusParams{
					Status:                current.Status,
					AuthorizationRevision: current.Revision,
					AuthorizationID:       lockedBinding.AuthorizationID,
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.CutoffStorageAuthorizationAccrual(
				ctx, dbgen.CutoffStorageAuthorizationAccrualParams{
					Cutoff:          pgTimestamp(cutoff),
					AuthorizationID: lockedBinding.AuthorizationID,
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.CloseStorageRetentionForSubmission(
				ctx, dbgen.CloseStorageRetentionForSubmissionParams{
					Cutoff:       pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.CloseReboundStorageAuthorization(
				ctx, dbgen.CloseReboundStorageAuthorizationParams{
					Status:                current.Status,
					AuthorizationRevision: current.Revision,
					ClosedAt:              pgTimestamp(cutoff),
					AuthorizationID:       lockedBinding.AuthorizationID,
				}); lockErr != nil {
				return lockErr
			}
			mapping, lockErr := queries.ReplaceStorageAuthorizationMapping(
				ctx, dbgen.ReplaceStorageAuthorizationMappingParams{
					AuthorizationID: toPGUUID(replacement.ID),
					AuthorizationRevision: pgtype.Int8{
						Int64: replacement.Revision, Valid: true,
					},
					ServiceIdentityID: toPGUUID(
						replacement.ServiceIdentityID),
					MeterID:      toPGUUID(replacement.MeterID),
					MaximumUnits: replacement.MaximumUnits,
					SubmissionID: toPGUUID(submissionID),
					ExpectedAuthorizationID: toPGUUID(
						expectedAuthorizationID),
					ExpectedMappingRevision: request.Msg.
						ExpectedMappingRevision.Value,
				})
			if lockErr != nil {
				return staleStorageAuthorizationMapping()
			}
			if _, lockErr = queries.CreateStorageAuthorizationBinding(
				ctx, dbgen.CreateStorageAuthorizationBindingParams{
					AuthorizationID:     toPGUUID(replacement.ID),
					SubmissionID:        toPGUUID(submissionID),
					MappingRevision:     mapping.MappingRevision,
					AuthorizerAccountID: toPGUUID(actor.accountID),
					OwnerKind:           scope.kind,
					OwnerID:             toPGUUID(scope.id),
					OrganizationID: toPGUUID(
						replacementOrganizationID),
					TeamID: toPGUUID(replacementTeamID),
					ServiceIdentityID: toPGUUID(
						replacement.ServiceIdentityID),
					MeterID:               toPGUUID(replacement.MeterID),
					MaximumUnits:          replacement.MaximumUnits,
					Status:                replacement.Status,
					AuthorizationRevision: replacement.Revision,
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.UpdateSubmissionStoragePayer(
				ctx, dbgen.UpdateSubmissionStoragePayerParams{
					OrganizationID: toPGUUID(replacementOrganizationID),
					TeamID:         toPGUUID(replacementTeamID),
					SubmissionID:   toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.ResolveStorageRecovery(
				ctx, dbgen.ResolveStorageRecoveryParams{
					RecoveredAt:        pgTimestamp(cutoff),
					TargetSubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			if _, lockErr = queries.BeginRetainedSubmissionStorage(
				ctx, dbgen.BeginRetainedSubmissionStorageParams{
					StartsAt:     pgTimestamp(cutoff),
					SubmissionID: toPGUUID(submissionID),
				}); lockErr != nil {
				return lockErr
			}
			completed, lockErr = queries.CompleteStorageRebindAttempt(
				ctx, dbgen.CompleteStorageRebindAttemptParams{
					ReplacementAuthorizationID: toPGUUID(
						replacement.ID),
					ReplacementAuthorizationRevision: pgtype.Int8{
						Int64: replacement.Revision, Valid: true,
					},
					ResultingMappingRevision: pgtype.Int8{
						Int64: mapping.MappingRevision, Valid: true,
					},
					CutoffAt:       pgTimestamp(cutoff),
					CallerDigest:   actor.digest,
					IdempotencyKey: toPGUUID(idempotencyID),
				})
			return lockErr
		})
	if err != nil {
		return nil, err
	}
	audit(ctx, service.dependencies, actor,
		"storage_authorization_rebound", scope, submissionID,
		"allow", "success")
	return storageRebindResponse(submissionID, completed, replayed)
}

func (service *Submission) prepareStorageRebindCutoff(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	recovery dbgen.RealqaStorageRecovery,
) error {
	cutoff := binding.AccrualCutoffAt.Time.UTC()
	if recovery.Reason != "billing_unavailable" {
		return service.settleStorageCutoff(ctx, binding, cutoff)
	}

	// A temporary reserve failure keeps the immediately preceding checkpoint
	// pending while the old grant remains retryable. Rebind ends that window:
	// an unreserved checkpoint becomes non-billable, while an already accepted
	// reservation must finish through its stored authorization/period before
	// the mapping can be replaced.
	periodStart := utcDayStart(cutoff.Add(-time.Nanosecond))
	queries := service.dependencies.Store.Queries()
	settlement, err := queries.GetStorageDailySettlement(
		ctx, dbgen.GetStorageDailySettlementParams{
			AuthorizationID: binding.AuthorizationID,
			PeriodStart:     pgTimestamp(periodStart),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if settlement.State == "pending" {
		if _, err = queries.SkipStorageDailySettlementForGrace(
			ctx, dbgen.SkipStorageDailySettlementForGraceParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     settlement.PeriodStart,
			}); err == nil {
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		settlement, err = queries.GetStorageDailySettlement(
			ctx, dbgen.GetStorageDailySettlementParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     settlement.PeriodStart,
			})
		if err != nil {
			return err
		}
	}
	switch settlement.State {
	case "reserved":
		return service.commitReservedStorage(
			ctx, binding, settlement, periodStart,
			periodStart.Add(24*time.Hour))
	case "committed", "released", "settled_zero", "grace_skipped":
		return nil
	default:
		return errors.New(
			"realqa storage billing: invalid rebind cutoff settlement")
	}
}

func storageRebindResponse(
	submissionID uuid.UUID,
	attempt dbgen.RealqaStorageRebindAttempt,
	replayed bool,
) (*connect.Response[realqav1.RebindSubmissionStorageAuthorizationResponse], error) {
	authorizationID, err := fromPGUUID(
		attempt.ReplacementAuthorizationID)
	if err != nil || !attempt.ResultingMappingRevision.Valid ||
		attempt.ResultingMappingRevision.Int64 <= 0 {
		return nil, errors.New(
			"realqa storage billing: invalid completed rebind")
	}
	return connect.NewResponse(
		&realqav1.RebindSubmissionStorageAuthorizationResponse{
			SubmissionId: &realqav1.UuidV7{
				Value: submissionID.String(),
			},
			AuthorizationId: &realqav1.UuidV7{
				Value: authorizationID.String(),
			},
			MappingRevision: revision(
				attempt.ResultingMappingRevision.Int64),
			Idempotency: &realqav1.IdempotencyResult{
				Replayed:              replayed,
				Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_REBIND_SUBMISSION_STORAGE_AUTHORIZATION,
				OriginallyCompletedAt: timestamp(attempt.CompletedAt),
			},
		}), nil
}

func staleStorageAuthorizationMapping() error {
	return rqerr.New(
		connect.CodeAborted,
		realqav1.ErrorReason_ERROR_REASON_STALE_STORAGE_AUTHORIZATION_MAPPING,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
		0,
	)
}

func storageAuthorizationSubstitution() error {
	return rqerr.New(
		connect.CodeFailedPrecondition,
		realqav1.ErrorReason_ERROR_REASON_STORAGE_AUTHORIZATION_SUBSTITUTION,
		realqav1.FailureClass_FAILURE_CLASS_CONFLICT,
		0,
	)
}
