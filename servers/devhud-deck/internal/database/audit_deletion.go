package database

import (
	"bytes"
	"context"
	"errors"
	"time"

	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (store *Store) Record(ctx context.Context, event audit.Event) error {
	if event.ID == uuid.Nil || event.ActorPseudonym == "" ||
		event.Type == 0 || event.ResourceType == 0 || event.Outcome == 0 {
		return errors.New("deck database: invalid audit event")
	}
	var targetHash []byte
	if len(event.TargetHash) > 0 {
		targetHash = append([]byte(nil), event.TargetHash...)
	}
	return store.queries.InsertAuditEvent(ctx, dbgen.InsertAuditEventParams{
		AuditID:        pgUUID(event.ID),
		EventType:      int16(event.Type),
		ActorPseudonym: event.ActorPseudonym,
		OwnerScope:     pgInt2(event.OwnerScope, event.OwnerScope != 0),
		TargetHash:     targetHash,
		ResourceType:   int16(event.ResourceType),
		ResourceID:     pgUUID(event.ResourceID),
		Outcome:        int16(event.Outcome),
		OccurredAt:     pgTime(event.OccurredAt),
	})
}

type DeletionTrigger int16

const (
	DeletionTriggerOwner DeletionTrigger = iota + 1
	DeletionTriggerAccountLifecycle
	DeletionTriggerOrganizationLifecycle
)

type DeleteFeatureDataParams struct {
	JobID      uuid.UUID
	ReplayKey  uuid.UUID
	TargetID   uuid.UUID
	TargetHash [32]byte
	Trigger    DeletionTrigger
	AcceptedAt time.Time
}

type DeletionResult struct {
	JobID      uuid.UUID
	AcceptedAt time.Time
	Replayed   bool
}

func (store *Store) DeleteFeatureData(
	ctx context.Context,
	params DeleteFeatureDataParams,
) (DeletionResult, error) {
	var result DeletionResult
	err := store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		replay, replayErr := queries.GetDeletionJobByReplayKey(ctx, pgUUID(params.ReplayKey))
		if replayErr == nil {
			if !bytes.Equal(replay.TargetHash, params.TargetHash[:]) ||
				replay.Trigger != int16(params.Trigger) {
				return ErrIdempotencyConflict
			}
			result = DeletionResult{
				JobID:      uuidValue(replay.DeletionJobID),
				AcceptedAt: replay.AcceptedAt.Time.UTC(),
				Replayed:   true,
			}
			return nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return replayErr
		}
		if err := queries.EnsureOwnerLock(ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if _, err := queries.LockOwner(ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if err := queries.InsertOwnerTombstone(ctx, dbgen.InsertOwnerTombstoneParams{
			TargetHash: params.TargetHash[:], AcceptedAt: pgTime(params.AcceptedAt),
		}); err != nil {
			return err
		}
		switch params.Trigger {
		case DeletionTriggerOwner:
			if err := queries.DeletePersonalFeatureData(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
			if err := queries.DeleteAccountDevices(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
			if err := queries.DeletePersonalConnection(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
		case DeletionTriggerAccountLifecycle:
			if err := queries.DeletePersonalFeatureData(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
			if err := queries.DeleteAccountDevices(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
			if err := queries.DeletePersonalConnection(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
			if err := queries.DeleteDeckAccount(ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
		case DeletionTriggerOrganizationLifecycle:
			if err := deleteOrganization(ctx, queries, params.TargetID); err != nil {
				return err
			}
		default:
			return errors.New("deck database: invalid deletion trigger")
		}
		job, err := queries.InsertDeletionJob(ctx, dbgen.InsertDeletionJobParams{
			DeletionJobID: pgUUID(params.JobID),
			ReplayKey:     pgUUID(params.ReplayKey),
			TargetHash:    params.TargetHash[:],
			Trigger:       int16(params.Trigger),
			AcceptedAt:    pgTime(params.AcceptedAt),
		})
		if err != nil {
			return err
		}
		result = DeletionResult{
			JobID:      uuidValue(job.DeletionJobID),
			AcceptedAt: job.AcceptedAt.Time.UTC(),
		}
		return nil
	})
	return result, err
}

func (store *Store) DeleteOrganizationFeatureData(
	ctx context.Context,
	params DeleteFeatureDataParams,
) (DeletionResult, error) {
	var result DeletionResult
	err := store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		replay, replayErr := queries.GetDeletionJobByReplayKey(ctx, pgUUID(params.ReplayKey))
		if replayErr == nil {
			if !bytes.Equal(replay.TargetHash, params.TargetHash[:]) ||
				replay.Trigger != int16(params.Trigger) {
				return ErrIdempotencyConflict
			}
			result = DeletionResult{
				JobID:      uuidValue(replay.DeletionJobID),
				AcceptedAt: replay.AcceptedAt.Time.UTC(),
				Replayed:   true,
			}
			return nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return replayErr
		}
		if err := queries.EnsureOwnerLock(ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if _, err := queries.LockOwner(ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if err := queries.InsertOwnerTombstone(ctx, dbgen.InsertOwnerTombstoneParams{
			TargetHash: params.TargetHash[:], AcceptedAt: pgTime(params.AcceptedAt),
		}); err != nil {
			return err
		}
		if params.Trigger == DeletionTriggerOwner {
			id := pgUUID(params.TargetID)
			if err := queries.DeleteOrganizationFeatureData(ctx, id); err != nil {
				return err
			}
			if err := queries.DeleteOrganizationConnection(ctx, id); err != nil {
				return err
			}
		} else {
			if err := deleteOrganization(ctx, queries, params.TargetID); err != nil {
				return err
			}
		}
		job, err := queries.InsertDeletionJob(ctx, dbgen.InsertDeletionJobParams{
			DeletionJobID: pgUUID(params.JobID), ReplayKey: pgUUID(params.ReplayKey),
			TargetHash: params.TargetHash[:], Trigger: int16(params.Trigger),
			AcceptedAt: pgTime(params.AcceptedAt),
		})
		if err != nil {
			return err
		}
		result = DeletionResult{
			JobID:      uuidValue(job.DeletionJobID),
			AcceptedAt: job.AcceptedAt.Time.UTC(),
		}
		return nil
	})
	return result, err
}

func deleteOrganization(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
) error {
	id := pgUUID(organizationID)
	if err := queries.DeleteOrganizationFeatureData(ctx, id); err != nil {
		return err
	}
	if err := queries.DeleteOrganizationConnection(ctx, id); err != nil {
		return err
	}
	if err := queries.DeleteOrganizationTeamMemberships(ctx, id); err != nil {
		return err
	}
	return queries.DeleteOrganizationMemberships(ctx, id)
}
