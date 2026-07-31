package service

import (
	"bytes"
	"context"
	"errors"
	"math"
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
	existingAttempt, lookupErr := service.dependencies.Store.Queries().
		GetStorageRebindAttempt(
			ctx, dbgen.GetStorageRebindAttemptParams{
				CallerDigest:   actor.digest,
				IdempotencyKey: toPGUUID(idempotencyID),
			})
	if lookupErr == nil {
		if existingAttempt.SubmissionID != toPGUUID(submissionID) ||
			!bytes.Equal(existingAttempt.RequestDigest, requestDigest) {
			return nil, idempotencyConflict()
		}
		if existingAttempt.State == "completed" {
			return storageRebindResponse(
				submissionID, existingAttempt, true)
		}
	} else if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return nil, lookupErr
	}
	if submission.State != "storage_billing_grace" {
		if lookupErr == nil {
			if err = service.closePendingStorageRebind(
				ctx, actor, scope, submissionID, idempotencyID,
				request.Msg, existingAttempt,
			); err != nil {
				return nil, err
			}
		}
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
	maximumUnits := existingAttempt.ReplacementMaximumUnits
	replacementServiceIdentityID :=
		existingAttempt.ReplacementServiceIdentityID
	replacementMeterID := existingAttempt.ReplacementMeterID
	if errors.Is(lookupErr, pgx.ErrNoRows) {
		meters, metersErr := service.dependencies.Billing.Meters(ctx)
		if metersErr != nil || validateBillingMeters(meters) != nil {
			return nil, storageAuthorizationFailed()
		}
		maximumUnits, err = retainedStorageMaximumUnits(
			ctx, service.dependencies.Store.Queries(), submission.ID)
		if err != nil || maximumUnits <= 0 {
			return nil, retentionStateConflict()
		}
		replacementServiceIdentityID =
			toPGUUID(meters.Storage.ServiceIdentityID)
		replacementMeterID = toPGUUID(meters.Storage.ID)
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
				SubmissionID:                 toPGUUID(submissionID),
				CallerDigest:                 actor.digest,
				IdempotencyKey:               toPGUUID(idempotencyID),
				RequestDigest:                requestDigest,
				ExpectedAuthorizationID:      toPGUUID(expectedAuthorizationID),
				ExpectedMappingRevision:      request.Msg.ExpectedMappingRevision.Value,
				ReplacementOrganizationID:    toPGUUID(replacementOrganizationID),
				ReplacementTeamID:            toPGUUID(replacementTeamID),
				ReplacementMaximumUnits:      maximumUnits,
				ReplacementServiceIdentityID: replacementServiceIdentityID,
				ReplacementMeterID:           replacementMeterID,
				RevokeIdempotencyKey:         toPGUUID(revokeKey),
				CreateIdempotencyKey:         toPGUUID(createKey),
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
	if _, err = storageRebindAttemptMeter(attempt); err != nil {
		return nil, idempotencyConflict()
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
		attempt.ReplacementMaximumUnits <= 0 ||
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
			lockedRecovery, lockErr := queries.GetActiveStorageRecovery(
				ctx, toPGUUID(submissionID))
			if lockErr != nil ||
				lockedRecovery.ID != recovery.ID ||
				lockedRecovery.AuthorizationID != binding.AuthorizationID {
				return staleStorageAuthorizationMapping()
			}
			if lockedAttempt.ReplacementMaximumUnits <= 0 {
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
			pendingCutoffPeriod, lockErr :=
				service.lockPendingStorageRebindCutoff(
					ctx, queries, lockedBinding, lockedRecovery)
			if lockErr != nil {
				return storageAuthorizationFailed()
			}
			current, billingErr :=
				service.dependencies.Billing.GetStorageAuthorization(
					ctx, StorageAuthorizationLookupRequest{
						AuthorizationID: expectedAuthorizationID,
						ForwardedBearer: forwardedBearer,
					})
			if validationErr := validateStorageRebindSource(
				current, lockedBinding, billingErr); validationErr != nil {
				return validationErr
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
			replacementRequest, lockErr :=
				storageRebindAuthorizationRequest(
					lockedAttempt, scope, submissionID,
					forwardedBearer)
			if lockErr != nil {
				return storageAuthorizationFailed()
			}
			replacement, billingErr :=
				service.dependencies.Billing.CreateStorageAuthorization(
					ctx, replacementRequest)
			if billingErr != nil || validateStorageAuthorization(
				replacement, replacementRequest, actor.accountID) != nil {
				return storageAuthorizationFailed()
			}
			if pendingCutoffPeriod.Valid {
				if _, lockErr = queries.SkipStorageDailySettlementForGrace(
					ctx, dbgen.SkipStorageDailySettlementForGraceParams{
						AuthorizationID: lockedBinding.AuthorizationID,
						PeriodStart:     pendingCutoffPeriod,
					}); lockErr != nil {
					return lockErr
				}
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

// closePendingStorageRebind recovers a replacement that may have been created
// downstream before the local install transaction was lost. Once the
// submission has left grace, that grant must be closed rather than installed.
// The attempt remains pending as the durable create replay recipe. Once the
// create response is known, a deletion-pending binding durably gives the M2M
// closure worker everything it needs if this request is lost again.
func (service *Submission) closePendingStorageRebind(
	ctx context.Context,
	actor caller,
	scope owner,
	submissionID uuid.UUID,
	idempotencyID uuid.UUID,
	request *realqav1.RebindSubmissionStorageAuthorizationRequest,
	attempt dbgen.RealqaStorageRebindAttempt,
) error {
	expectedAuthorizationID, expectedErr := parseUUIDMessage(
		request.ExpectedAuthorizationId)
	replacementOrganizationID, organizationErr := parseUUIDMessage(
		request.ReplacementBilling.OrganizationId)
	replacementTeamID, teamErr := parseUUIDMessage(
		request.ReplacementBilling.TeamId)
	revokeKey, revokeErr := derivedUUIDv7(
		idempotencyID, "storage-rebind-revoke")
	createKey, createErr := derivedUUIDv7(
		idempotencyID, "storage-rebind-create")
	if expectedErr != nil || organizationErr != nil || teamErr != nil ||
		revokeErr != nil || createErr != nil ||
		attempt.State != "pending" ||
		attempt.ExpectedAuthorizationID != toPGUUID(expectedAuthorizationID) ||
		attempt.ExpectedMappingRevision != request.ExpectedMappingRevision.Value ||
		attempt.ReplacementOrganizationID !=
			toPGUUID(replacementOrganizationID) ||
		attempt.ReplacementTeamID != toPGUUID(replacementTeamID) ||
		attempt.RevokeIdempotencyKey != toPGUUID(revokeKey) ||
		attempt.CreateIdempotencyKey != toPGUUID(createKey) {
		return idempotencyConflict()
	}
	if service.dependencies.Billing == nil {
		return storageAuthorizationFailed()
	}
	forwardedBearer, ok := service.forwardedBearer(ctx)
	if !ok {
		return reauthenticationRequired()
	}
	replacementRequest, err := storageRebindAuthorizationRequest(
		attempt, scope, submissionID, forwardedBearer)
	if err != nil {
		return storageAuthorizationFailed()
	}
	replacement, err := service.dependencies.Billing.
		CreateStorageAuthorization(ctx, replacementRequest)
	if err != nil ||
		validateStorageAuthorization(
			replacement, replacementRequest, actor.accountID) != nil {
		return storageAuthorizationFailed()
	}
	cleanupBinding := dbgen.RealqaStorageAuthorizationBinding{
		AuthorizationID:       toPGUUID(replacement.ID),
		SubmissionID:          toPGUUID(submissionID),
		MappingRevision:       attempt.ExpectedMappingRevision + 1,
		AuthorizerAccountID:   toPGUUID(actor.accountID),
		OwnerKind:             scope.kind,
		OwnerID:               toPGUUID(scope.id),
		OrganizationID:        toPGUUID(replacementOrganizationID),
		TeamID:                toPGUUID(replacementTeamID),
		ServiceIdentityID:     attempt.ReplacementServiceIdentityID,
		MeterID:               attempt.ReplacementMeterID,
		MaximumUnits:          attempt.ReplacementMaximumUnits,
		Status:                replacement.Status,
		AuthorizationRevision: replacement.Revision,
	}
	alreadyClosed := false
	err = service.dependencies.Store.WithinTransaction(
		ctx, pgx.TxOptions{}, func(queries *dbgen.Queries) error {
			persisted, persistErr :=
				queries.CreateStorageAuthorizationBinding(
					ctx, dbgen.CreateStorageAuthorizationBindingParams{
						AuthorizationID: cleanupBinding.AuthorizationID,
						SubmissionID:    cleanupBinding.SubmissionID,
						MappingRevision: cleanupBinding.MappingRevision,
						AuthorizerAccountID: cleanupBinding.
							AuthorizerAccountID,
						OwnerKind:      cleanupBinding.OwnerKind,
						OwnerID:        cleanupBinding.OwnerID,
						OrganizationID: cleanupBinding.OrganizationID,
						TeamID:         cleanupBinding.TeamID,
						ServiceIdentityID: cleanupBinding.
							ServiceIdentityID,
						MeterID: cleanupBinding.MeterID,
						MaximumUnits: cleanupBinding.
							MaximumUnits,
						Status: cleanupBinding.Status,
						AuthorizationRevision: cleanupBinding.
							AuthorizationRevision,
					})
			if errors.Is(persistErr, pgx.ErrNoRows) {
				persisted, persistErr =
					queries.GetStorageAuthorizationBinding(
						ctx, cleanupBinding.AuthorizationID)
			}
			if persistErr != nil {
				return persistErr
			}
			if !sameStorageAuthorizationBinding(
				persisted, cleanupBinding) {
				return storageAuthorizationSubstitution()
			}
			if persisted.ClosureState == "closed" {
				if !storageClosureStatusAllowed(
					persisted.Status, false) ||
					persisted.AuthorizationRevision <
						replacement.Revision {
					return storageAuthorizationFailed()
				}
				alreadyClosed = true
				return nil
			}
			_, persistErr =
				queries.MarkStorageAuthorizationClosurePending(
					ctx, dbgen.MarkStorageAuthorizationClosurePendingParams{
						Cutoff: pgTimestamp(
							service.dependencies.Clock.Now().UTC()),
						AuthorizationID: cleanupBinding.
							AuthorizationID,
					})
			return persistErr
		})
	if err != nil {
		return err
	}
	if alreadyClosed {
		return nil
	}
	closeKey, err := derivedUUIDv7(
		idempotencyID, "storage-rebind-resource-deleted")
	if err != nil {
		return err
	}
	closed, err := service.dependencies.Billing.MarkStorageResourceDeleted(
		ctx, StorageResourceDeletedRequest{
			AuthorizationID:   replacement.ID,
			FeatureResourceID: submissionID,
			ExpectedRevision:  replacement.Revision,
			IdempotencyKey:    closeKey,
		})
	if err != nil ||
		validateBoundStorageAuthorization(
			closed, cleanupBinding, false) != nil ||
		!storageClosureStatusAllowed(closed.Status, false) {
		return storageAuthorizationFailed()
	}
	if _, err = service.dependencies.Store.Queries().
		CompleteStorageAuthorizationClosure(
			ctx, dbgen.CompleteStorageAuthorizationClosureParams{
				Status:                closed.Status,
				AuthorizationRevision: closed.Revision,
				AuthorizationID:       cleanupBinding.AuthorizationID,
			}); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		persisted, lookupErr := service.dependencies.Store.Queries().
			GetStorageAuthorizationBinding(
				ctx, cleanupBinding.AuthorizationID)
		if lookupErr != nil ||
			!sameStorageAuthorizationBinding(
				persisted, cleanupBinding) ||
			persisted.ClosureState != "closed" ||
			persisted.AuthorizationRevision < closed.Revision ||
			!storageClosureStatusAllowed(persisted.Status, false) {
			return storageAuthorizationFailed()
		}
	}
	audit(ctx, service.dependencies, actor,
		"storage_rebind_replacement_closed", scope, submissionID,
		"allow", "success")
	return nil
}

func sameStorageAuthorizationBinding(
	actual dbgen.RealqaStorageAuthorizationBinding,
	expected dbgen.RealqaStorageAuthorizationBinding,
) bool {
	return actual.AuthorizationID == expected.AuthorizationID &&
		actual.SubmissionID == expected.SubmissionID &&
		actual.MappingRevision == expected.MappingRevision &&
		actual.AuthorizerAccountID == expected.AuthorizerAccountID &&
		actual.OwnerKind == expected.OwnerKind &&
		actual.OwnerID == expected.OwnerID &&
		actual.OrganizationID == expected.OrganizationID &&
		actual.TeamID == expected.TeamID &&
		actual.ServiceIdentityID == expected.ServiceIdentityID &&
		actual.MeterID == expected.MeterID &&
		actual.MaximumUnits == expected.MaximumUnits
}

func retainedStorageMaximumUnits(
	ctx context.Context,
	queries dbgen.Querier,
	submissionID pgtype.UUID,
) (int64, error) {
	assets, err := queries.ListSubmissionAssets(ctx, submissionID)
	if err != nil {
		return 0, err
	}
	var retainedBytes int64
	for _, asset := range assets {
		if asset.State != "public_retained" {
			continue
		}
		if asset.UploadState != "verified" || asset.EncodedBytes <= 0 ||
			retainedBytes > math.MaxInt64-asset.EncodedBytes {
			return 0, errors.New(
				"realqa storage billing: invalid retained asset bytes")
		}
		retainedBytes += asset.EncodedBytes
	}
	return ceilMiB(retainedBytes)
}

func storageRebindAuthorizationRequest(
	attempt dbgen.RealqaStorageRebindAttempt,
	scope owner,
	submissionID uuid.UUID,
	forwardedBearer string,
) (StorageAuthorizationRequest, error) {
	organizationID, organizationErr := fromPGUUID(
		attempt.ReplacementOrganizationID)
	teamID, teamErr := fromPGUUID(attempt.ReplacementTeamID)
	createKey, createKeyErr := fromPGUUID(attempt.CreateIdempotencyKey)
	meter, meterErr := storageRebindAttemptMeter(attempt)
	if organizationErr != nil || teamErr != nil || createKeyErr != nil ||
		meterErr != nil ||
		attempt.ReplacementMaximumUnits <= 0 {
		return StorageAuthorizationRequest{}, errors.New(
			"realqa storage billing: invalid persisted rebind input")
	}
	return StorageAuthorizationRequest{
		OwnerKind:         scope.kind,
		OwnerID:           scope.id,
		OrganizationID:    organizationID,
		TeamID:            teamID,
		ServiceIdentityID: meter.ServiceIdentityID,
		MeterID:           meter.ID,
		FeatureResourceID: submissionID,
		MaximumUnits:      attempt.ReplacementMaximumUnits,
		IdempotencyKey:    createKey,
		ForwardedBearer:   forwardedBearer,
	}, nil
}

func storageRebindAttemptMeter(
	attempt dbgen.RealqaStorageRebindAttempt,
) (BillingMeter, error) {
	serviceIdentityID, serviceIdentityErr := fromPGUUID(
		attempt.ReplacementServiceIdentityID)
	meterID, meterErr := fromPGUUID(attempt.ReplacementMeterID)
	if serviceIdentityErr != nil || meterErr != nil {
		return BillingMeter{}, errors.New(
			"realqa storage billing: invalid persisted rebind meter")
	}
	return BillingMeter{
		ID:                meterID,
		ServiceIdentityID: serviceIdentityID,
	}, nil
}

func (service *Submission) prepareStorageRebindCutoff(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	recovery dbgen.RealqaStorageRecovery,
) error {
	cutoff := binding.AccrualCutoffAt.Time.UTC()
	if recovery.Reason != "billing_unavailable" {
		periodStart := utcDayStart(cutoff)
		if !cutoff.After(periodStart) {
			return service.settleCompletedStorageRebindCutoff(
				ctx, binding, cutoff.Add(-time.Nanosecond))
		}
		today := utcDayStart(service.dependencies.Clock.Now())
		if periodStart.Before(today.Add(-24 * time.Hour)) {
			authorizationID, err := fromPGUUID(binding.AuthorizationID)
			if err != nil {
				return err
			}
			if err = service.processStoragePeriod(
				ctx, authorizationID, periodStart, today); err != nil {
				return err
			}
			return nil
		}
		return service.settleStorageCutoff(ctx, binding, cutoff)
	}

	// A temporary reserve failure keeps the immediately preceding checkpoint
	// pending while the old grant remains retryable. An already accepted
	// reservation must finish through its stored authorization/period before
	// the mapping can be replaced. The still-pending checkpoint is locked and
	// skipped only in the replacement transaction after downstream success.
	return service.finishReservedStorageRebindCutoff(
		ctx, binding, cutoff.Add(-time.Nanosecond))
}

// settleCompletedStorageRebindCutoff runs the completed-day path so a
// missing or pending checkpoint is durably settled or skipped before the old
// authorization mapping can be replaced.
func (service *Submission) settleCompletedStorageRebindCutoff(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	cutoff time.Time,
) error {
	periodStart := utcDayStart(cutoff)
	settlement, err := service.dependencies.Store.Queries().
		GetStorageDailySettlement(
			ctx, dbgen.GetStorageDailySettlementParams{
				AuthorizationID: binding.AuthorizationID,
				PeriodStart:     pgTimestamp(periodStart),
			})
	if err == nil && settlement.State != "pending" {
		return service.finishReservedStorageRebindCutoff(
			ctx, binding, cutoff)
	}
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	authorizationID, err := fromPGUUID(binding.AuthorizationID)
	if err != nil {
		return err
	}
	processingDay, err := storageCutoffProcessingDay(
		service.dependencies.Clock.Now(), periodStart)
	if err != nil {
		return err
	}
	return service.processStoragePeriod(
		ctx, authorizationID, periodStart, processingDay)
}

func (service *Submission) finishReservedStorageRebindCutoff(
	ctx context.Context,
	binding dbgen.RealqaStorageAuthorizationBinding,
	cutoff time.Time,
) error {
	periodStart := utcDayStart(cutoff)
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
	switch settlement.State {
	case "pending":
		return nil
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

func (service *Submission) lockPendingStorageRebindCutoff(
	ctx context.Context,
	queries *dbgen.Queries,
	binding dbgen.RealqaStorageAuthorizationBinding,
	recovery dbgen.RealqaStorageRecovery,
) (pgtype.Timestamptz, error) {
	if recovery.Reason != "billing_unavailable" ||
		!binding.AccrualCutoffAt.Valid {
		return pgtype.Timestamptz{}, nil
	}
	periodStart := utcDayStart(
		binding.AccrualCutoffAt.Time.UTC().Add(-time.Nanosecond))
	settlement, err := queries.LockStorageDailySettlement(
		ctx, dbgen.LockStorageDailySettlementParams{
			AuthorizationID: binding.AuthorizationID,
			PeriodStart:     pgTimestamp(periodStart),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return pgtype.Timestamptz{}, nil
	}
	if err != nil {
		return pgtype.Timestamptz{}, err
	}
	switch settlement.State {
	case "pending":
		return settlement.PeriodStart, nil
	case "committed", "released", "settled_zero", "grace_skipped":
		return pgtype.Timestamptz{}, nil
	default:
		return pgtype.Timestamptz{}, errors.New(
			"realqa storage billing: rebind cutoff changed")
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

func validateStorageRebindSource(
	authorization StorageAuthorization,
	binding dbgen.RealqaStorageAuthorizationBinding,
	lookupErr error,
) error {
	if lookupErr != nil {
		return storageAuthorizationFailed()
	}
	if validateBoundStorageAuthorization(
		authorization, binding, false) != nil {
		return storageAuthorizationSubstitution()
	}
	return nil
}
