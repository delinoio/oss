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
	"google.golang.org/protobuf/types/known/timestamppb"
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
		if _, err = authorizeOwner(ctx, service.dependencies, actor, scope,
			true, scope.kind == "organization"); err != nil {
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

	if existing, getErr := service.dependencies.Store.Queries().GetDeletionJob(
		ctx, dbgen.GetDeletionJobParams{
			OwnerKind: scope.kind, OwnerID: toPGUUID(scope.id),
		}); getErr == nil {
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
		return deletionReplay(existingID, existing.AcceptedAt), nil
	} else if !errors.Is(getErr, pgx.ErrNoRows) {
		return nil, getErr
	}

	var removed int64
	idempotencyRecordID, err := newID(service.dependencies)
	if err != nil {
		return nil, err
	}
	err = service.dependencies.Store.WithinTransaction(ctx, pgx.TxOptions{},
		func(queries *dbgen.Queries) error {
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
			count, deleteErr := queries.DeleteScopeIdempotencySnapshots(ctx,
				dbgen.DeleteScopeIdempotencySnapshotsParams{
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
				count, deleteErr = queries.TombstoneLifecycleAccountIdentity(
					ctx, toPGUUID(scope.id))
				removed += count
				if deleteErr != nil {
					return deleteErr
				}
			}
			if _, insertErr := queries.InsertDeletionJob(ctx,
				dbgen.InsertDeletionJobParams{
					ID: toPGUUID(jobID), OwnerKind: scope.kind,
					OwnerID: toPGUUID(scope.id), TriggerKind: trigger,
				}); insertErr != nil {
				return insertErr
			}
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
			existingID, _ := fromPGUUID(existing.ID)
			return deletionReplay(existingID, existing.AcceptedAt), nil
		}
		return nil, err
	}
	audit(ctx, service.dependencies, actor, "feature_deletion_accepted",
		scope, jobID, "allow", "success")
	return connect.NewResponse(&realqav1.DeleteFeatureDataResponse{
		DeletionJobId: &realqav1.UuidV7{Value: jobID.String()},
		Accepted:      true, AlreadyAbsent: removed == 0,
		Idempotency: &realqav1.IdempotencyResult{
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_FEATURE_DATA,
			OriginallyCompletedAt: timestamppb.New(service.dependencies.Clock.Now()),
		},
	}), nil
}

func deletionReplay(
	jobID uuid.UUID,
	completed pgtype.Timestamptz,
) *connect.Response[realqav1.DeleteFeatureDataResponse] {
	return connect.NewResponse(&realqav1.DeleteFeatureDataResponse{
		DeletionJobId: &realqav1.UuidV7{Value: jobID.String()},
		Accepted:      true, AlreadyAbsent: true,
		Idempotency: &realqav1.IdempotencyResult{
			Replayed:              true,
			Operation:             realqav1.IdempotentOperation_IDEMPOTENT_OPERATION_DELETE_FEATURE_DATA,
			OriginallyCompletedAt: timestamp(completed),
		},
	})
}
