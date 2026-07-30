package service

import (
	"bytes"
	"context"
	"errors"

	"connectrpc.com/connect"
	realqav1 "github.com/delinoio/oss/protos/devhud-realqa/gen/go/devhud-realqa/v1"
	"github.com/delinoio/oss/servers/devhud-realqa/internal/database/dbgen"
	"github.com/delinoio/oss/servers/internal/auth"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func (service *Preset) DeleteFeatureData(
	ctx context.Context,
	request *connect.Request[realqav1.DeleteFeatureDataRequest],
) (*connect.Response[realqav1.DeleteFeatureDataResponse], error) {
	if request == nil || request.Msg == nil {
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	var (
		actor            caller
		scope            owner
		jobID            uuid.UUID
		trigger          string
		idempotencyID    uuid.UUID
		requestDigest    []byte
		ownerRequest     bool
		accountLifecycle bool
		err              error
	)
	switch request.Msg.TriggerKind {
	case realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_OWNER_REQUEST:
		ownerRequest = true
		if request.Msg.GetOwnerRequest() == nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		actor, err = resolveCaller(ctx, service.dependencies)
		if err != nil {
			return nil, err
		}
		scope, err = parseOwner(request.Msg.GetOwnerRequest().Owner)
		if err != nil {
			return nil, err
		}
		idempotencyID, err = parseIdempotency(request.Msg.GetOwnerRequest().Idempotency)
		if err != nil {
			return nil, err
		}
		requestDigest, err = digestMessage(request.Msg)
		if err != nil {
			return nil, err
		}
		record, lookupErr := service.dependencies.Store.Queries().GetIdempotencyRecord(
			ctx, idempotencyLookupFor(actor, idempotencyID, "delete_feature_data"))
		if lookupErr == nil {
			if !bytes.Equal(record.RequestDigest, requestDigest) {
				return nil, idempotencyConflict()
			}
			existingID, conversionErr := fromPGUUID(record.ResourceID)
			if conversionErr != nil {
				return nil, conversionErr
			}
			existing, getErr := service.dependencies.Store.Queries().GetDeletionJob(
				ctx, dbgen.GetDeletionJobParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			if getErr != nil {
				return nil, getErr
			}
			return deletionReplay(
				existingID, record.CompletedAt, existing.AlreadyAbsent,
			), nil
		}
		if !errors.Is(lookupErr, pgx.ErrNoRows) {
			return nil, lookupErr
		}
		if _, err = authorizeOwner(ctx, service.dependencies, actor, scope,
			true, scope.kind == "organization"); err != nil {
			return nil, err
		}
		jobID, err = newID(service.dependencies)
		if err != nil {
			return nil, err
		}
		trigger = "owner_request"
	case realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ACCOUNT_LIFECYCLE:
		accountLifecycle = true
		value := request.Msg.GetDelibaseAccountLifecycle()
		if value == nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		scope.id, err = parseUUIDMessage(value.AccountId)
		if err != nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		scope.kind = "personal"
		jobID, err = parseUUIDMessage(value.DeletionJobId)
		if err != nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		trigger = "delibase_account_lifecycle"
	case realqav1.FeatureDeletionTriggerKind_FEATURE_DELETION_TRIGGER_KIND_DELIBASE_ORGANIZATION_LIFECYCLE:
		value := request.Msg.GetDelibaseOrganizationLifecycle()
		if value == nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		scope.id, err = parseUUIDMessage(value.OrganizationId)
		if err != nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		scope.kind = "organization"
		jobID, err = parseUUIDMessage(value.DeletionJobId)
		if err != nil {
			return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
		}
		trigger = "delibase_organization_lifecycle"
	default:
		return nil, invalid(realqav1.ErrorReason_ERROR_REASON_PROVIDER_VALIDATION_FAILED)
	}
	if !ownerRequest {
		principal, ok := auth.PrincipalFromContext(ctx)
		if !ok || principal.M2M == nil {
			return nil, permissionDenied()
		}
		actor = caller{actor: ""}
	}
	if service.dependencies.Store == nil {
		return nil, errors.New("realqa service: store unavailable")
	}

	if existing, getErr := service.dependencies.Store.Queries().GetDeletionJob(
		ctx, dbgen.GetDeletionJobParams{
			OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
		}); getErr == nil {
		if accountLifecycle {
			if cleanupErr := service.disconnectLifecycleAccount(
				ctx, scope.id,
			); cleanupErr != nil {
				return nil, cleanupErr
			}
		}
		if !ownerRequest {
			imageService := NewSubmission(service.dependencies)
			if cleanupErr := imageService.
				HandleLifecycleAuthorizationDeletion(
					ctx, scope, existing.AcceptedAt.Time,
				); cleanupErr != nil {
				return nil, cleanupErr
			}
		}
		existingID, conversionErr := fromPGUUID(existing.ID)
		if conversionErr != nil {
			return nil, conversionErr
		}
		if !ownerRequest && existingID != jobID {
			// Once a scope is tombstoned, later lifecycle deliveries are absent
			// successes but may not replace its immutable first deletion identity.
			return connect.NewResponse(&realqav1.DeleteFeatureDataResponse{
				DeletionJobId: &realqav1.UuidV7{Value: existingID.String()},
				Accepted:      true, AlreadyAbsent: true,
			}), nil
		}
		return deletionReplay(
			existingID, existing.AcceptedAt, existing.AlreadyAbsent,
		), nil
	} else if !errors.Is(getErr, pgx.ErrNoRows) {
		return nil, getErr
	}

	var removed int64
	var deletionJob dbgen.RealqaDeletionJob
	var scopedAssets []dbgen.RealqaAsset
	idempotencyRecordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			tombstoned, lockErr := lockOwnerScope(ctx, queries, scope)
			if lockErr != nil {
				return lockErr
			}
			if tombstoned {
				return errIdempotencyReplay
			}
			if ownerRequest {
				record, lookupErr := queries.GetIdempotencyRecord(
					ctx, dbgen.GetIdempotencyRecordParams{
						CallerKind: "user", CallerDigest: actor.digest,
						Operation:      "delete_feature_data",
						IdempotencyKey: toPGUUID(idempotencyID),
					})
				if lookupErr == nil {
					if !bytes.Equal(record.RequestDigest, requestDigest) {
						return idempotencyConflict()
					}
					return errIdempotencyReplay
				}
				if !errors.Is(lookupErr, pgx.ErrNoRows) {
					return lookupErr
				}
			}
			if insertErr := queries.InsertScopeTombstone(ctx,
				dbgen.InsertScopeTombstoneParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
					DeletionJobID: toPGUUID(jobID), TriggerKind: trigger,
				}); insertErr != nil {
				return insertErr
			}
			var listErr error
			if _, listErr = queries.LockScopeSubmissionRecords(
				ctx, dbgen.LockScopeSubmissionRecordsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				}); listErr != nil {
				return listErr
			}
			scopedAssets, listErr = queries.ListScopeObjectAssets(ctx,
				dbgen.ListScopeObjectAssetsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			if listErr != nil {
				return listErr
			}
			for _, asset := range scopedAssets {
				if listErr = enqueueAssetObjectDeletions(
					ctx, queries, asset); listErr != nil {
					return listErr
				}
			}
			if listErr = queries.TombstoneScopePublicAssets(ctx,
				dbgen.TombstoneScopePublicAssetsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				}); listErr != nil {
				return listErr
			}
			cutoff, listErr := queries.GetTransactionTimestamp(ctx)
			if listErr != nil {
				return listErr
			}
			if _, listErr = queries.CloseStorageRetentionForScope(
				ctx, dbgen.CloseStorageRetentionForScopeParams{
					Cutoff:    cutoff,
					OwnerKind: scope.kind,
					OwnerID:   toPGUUID(scope.id),
				}); listErr != nil {
				return listErr
			}
			if _, listErr = queries.MarkScopeStorageClosurePending(
				ctx, dbgen.MarkScopeStorageClosurePendingParams{
					OwnerDeletedAllowed: !ownerRequest,
					Cutoff:              cutoff,
					OwnerKind:           scope.kind,
					OwnerID:             toPGUUID(scope.id),
				}); listErr != nil {
				return listErr
			}
			count, deleteErr := queries.DeleteScopeDisconnectIdempotencySnapshots(ctx,
				dbgen.DeleteScopeDisconnectIdempotencySnapshotsParams{
					ScopeOwnerKind: scope.kind, ScopeOwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopePresetIdempotencySnapshots(ctx,
				dbgen.DeleteScopePresetIdempotencySnapshotsParams{
					ScopeOwnerKind: scope.kind, ScopeOwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeSubmissionIdempotencySnapshots(ctx,
				dbgen.DeleteScopeSubmissionIdempotencySnapshotsParams{
					ScopeOwnerKind: scope.kind, ScopeOwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopePresets(ctx,
				dbgen.DeleteScopePresetsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeSubmissions(ctx,
				dbgen.DeleteScopeSubmissionsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeBillingIssueAttempts(
				ctx, dbgen.DeleteScopeBillingIssueAttemptsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeBillingAssets(
				ctx, dbgen.DeleteScopeBillingAssetsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.MinimizeScopeBillingSubmissions(
				ctx, dbgen.MinimizeScopeBillingSubmissionsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeDestinations(ctx,
				dbgen.DeleteScopeDestinationsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			count, deleteErr = queries.DeleteScopeConnections(ctx,
				dbgen.DeleteScopeConnectionsParams{
					OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
				})
			removed += count
			if deleteErr != nil {
				return deleteErr
			}
			if accountLifecycle {
				count, deleteErr = queries.DeleteLifecycleAccountRepositoryAccess(
					ctx, toPGUUID(scope.id))
				removed += count
				if deleteErr != nil {
					return deleteErr
				}
				count, deleteErr = queries.DisconnectGitHubConnectionsForAccount(
					ctx, toPGUUID(scope.id))
				removed += count
				if deleteErr != nil {
					return deleteErr
				}
				count, deleteErr = queries.TombstoneLifecycleAccountIdentity(
					ctx, toPGUUID(scope.id))
				removed += count
				if deleteErr != nil {
					return deleteErr
				}
			}
			insertedJob, insertErr := queries.InsertDeletionJob(ctx,
				dbgen.InsertDeletionJobParams{
					ID: toPGUUID(jobID), OwnerKind: scope.kind,
					OwnerID: toPGUUID(scope.id), TriggerKind: trigger,
					AlreadyAbsent: removed == 0,
				})
			if insertErr != nil {
				return insertErr
			}
			deletionJob = insertedJob
			if ownerRequest {
				_, insertErr := queries.CreateIdempotencyRecord(ctx,
					dbgen.CreateIdempotencyRecordParams{
						ID: toPGUUID(idempotencyRecordID), CallerKind: "user",
						CallerDigest: actor.digest, Operation: "delete_feature_data",
						IdempotencyKey: toPGUUID(idempotencyID),
						RequestDigest:  requestDigest, ResourceID: toPGUUID(jobID),
					})
				return insertErr
			}
			return nil
		})
	if err != nil {
		if existing, getErr := service.dependencies.Store.Queries().GetDeletionJob(
			ctx, dbgen.GetDeletionJobParams{
				OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
			}); getErr == nil {
			if accountLifecycle {
				if cleanupErr := service.disconnectLifecycleAccount(
					ctx, scope.id,
				); cleanupErr != nil {
					return nil, cleanupErr
				}
			}
			if !ownerRequest {
				imageService := NewSubmission(service.dependencies)
				if cleanupErr := imageService.
					HandleLifecycleAuthorizationDeletion(
						ctx, scope, existing.AcceptedAt.Time,
					); cleanupErr != nil {
					return nil, cleanupErr
				}
			}
			existingID, _ := fromPGUUID(existing.ID)
			return deletionReplay(
				existingID, existing.AcceptedAt, existing.AlreadyAbsent,
			), nil
		}
		return nil, err
	}
	imageService := NewSubmission(service.dependencies)
	if !ownerRequest {
		if err = imageService.HandleLifecycleAuthorizationDeletion(
			ctx, scope, deletionJob.AcceptedAt.Time); err != nil {
			return nil, err
		}
	}
	imageService.drainObjectDeletionsBestEffort(context.WithoutCancel(ctx))
	audit(ctx, service.dependencies, actor, "feature_deletion_accepted",
		scope, jobID, "allow", "success")
	return connect.NewResponse(&realqav1.DeleteFeatureDataResponse{
		DeletionJobId: &realqav1.UuidV7{Value: jobID.String()},
		Accepted:      true, AlreadyAbsent: removed == 0,
		Idempotency: &realqav1.IdempotencyResult{
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_FEATURE_DATA,
			OriginallyCompletedAt: timestamp(deletionJob.AcceptedAt),
		},
	}), nil
}

func (service *Preset) disconnectLifecycleAccount(
	ctx context.Context,
	accountID uuid.UUID,
) error {
	return service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
			if _, err := queries.DeleteLifecycleAccountRepositoryAccess(
				ctx, toPGUUID(accountID),
			); err != nil {
				return err
			}
			if _, err := queries.DisconnectGitHubConnectionsForAccount(
				ctx, toPGUUID(accountID),
			); err != nil {
				return err
			}
			_, err := queries.TombstoneLifecycleAccountIdentity(
				ctx, toPGUUID(accountID))
			return err
		})
}

func deletionReplay(
	jobID uuid.UUID,
	completed pgtype.Timestamptz,
	alreadyAbsent bool,
) *connect.Response[realqav1.DeleteFeatureDataResponse] {
	return connect.NewResponse(&realqav1.DeleteFeatureDataResponse{
		DeletionJobId: &realqav1.UuidV7{Value: jobID.String()},
		Accepted:      true, AlreadyAbsent: alreadyAbsent,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              true,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_FEATURE_DATA,
			OriginallyCompletedAt: timestamp(completed),
		},
	})
}
