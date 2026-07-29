package database

import (
	"bytes"
	"context"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/audit"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	shortcutbinding "github.com/delinoio/oss/servers/devhud-deck/internal/shortcut"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
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
		if err := queries.DeleteViewCreateIdempotencyByOwnerHash(
			ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if params.Trigger != DeletionTriggerOrganizationLifecycle {
			if err := queries.DeleteGitHubCallbackStatesByOwner(
				ctx, dbgen.DeleteGitHubCallbackStatesByOwnerParams{
					OwnerScope: 1, OwnerID: pgUUID(params.TargetID),
				}); err != nil {
				return err
			}
			if err := queries.DeleteGitHubUserCredentialsByAccount(
				ctx, pgUUID(params.TargetID)); err != nil {
				return err
			}
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
			if err := store.deleteOrganization(
				ctx, queries, params.TargetID, params.AcceptedAt, true); err != nil {
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
		if err := queries.DeleteViewCreateIdempotencyByOwnerHash(
			ctx, params.TargetHash[:]); err != nil {
			return err
		}
		if params.Trigger == DeletionTriggerOwner {
			if err := store.deleteOrganization(
				ctx, queries, params.TargetID, params.AcceptedAt, false); err != nil {
				return err
			}
		} else {
			if err := store.deleteOrganization(
				ctx, queries, params.TargetID, params.AcceptedAt, true); err != nil {
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

func (store *Store) deleteOrganization(
	ctx context.Context,
	queries *dbgen.Queries,
	organizationID uuid.UUID,
	deletedAt time.Time,
	deleteMemberships bool,
) error {
	id := pgUUID(organizationID)
	if err := queries.DeleteGitHubCallbackStatesByOwner(
		ctx, dbgen.DeleteGitHubCallbackStatesByOwnerParams{
			OwnerScope: 2, OwnerID: id,
		}); err != nil {
		return err
	}
	viewIDs, err := queries.ListOrganizationViewIDsForUpdate(ctx, id)
	if err != nil {
		return err
	}
	if err := store.scrubDeviceViewState(ctx, queries, viewIDs, deletedAt); err != nil {
		return err
	}
	if err := queries.DeleteOrganizationFeatureData(ctx, id); err != nil {
		return err
	}
	if err := queries.DeleteOrganizationConnection(ctx, id); err != nil {
		return err
	}
	if !deleteMemberships {
		return nil
	}
	if err := queries.DeleteOrganizationTeamMemberships(ctx, id); err != nil {
		return err
	}
	return queries.DeleteOrganizationMemberships(ctx, id)
}

func (store *Store) scrubDeviceViewState(
	ctx context.Context,
	queries *dbgen.Queries,
	viewIDs []pgtype.UUID,
	deletedAt time.Time,
) error {
	if len(viewIDs) == 0 {
		return nil
	}
	deleted := make(map[uuid.UUID]struct{}, len(viewIDs))
	for _, viewID := range viewIDs {
		deleted[uuidValue(viewID)] = struct{}{}
	}
	devices, err := queries.ListDeviceRegistrationsForUpdate(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		shortcuts := &deckv1.Device{}
		if err := store.openProto(
			"device-shortcuts", device.ShortcutsCiphertext, shortcuts); err != nil {
			return err
		}
		widgets := &deckv1.Device{}
		if err := store.openProto(
			"device-widgets", device.WidgetsCiphertext, widgets); err != nil {
			return err
		}
		remainingShortcuts, shortcutsChanged := retainShortcuts(
			shortcuts.Shortcuts, deleted)
		remainingWidgets, widgetsChanged := retainWidgets(widgets.Widgets, deleted)
		if !shortcutsChanged && !widgetsChanged {
			continue
		}
		shortcuts.Shortcuts = remainingShortcuts
		widgets.Widgets = remainingWidgets
		if err := recalculateShortcutStates(shortcuts.Shortcuts); err != nil {
			return err
		}
		shortcutsCiphertext, err := store.sealProto("device-shortcuts", shortcuts)
		if err != nil {
			return err
		}
		widgetsCiphertext, err := store.sealProto("device-widgets", widgets)
		if err != nil {
			return err
		}
		if err := queries.UpdateDeviceViewStateAfterDeletion(ctx,
			dbgen.UpdateDeviceViewStateAfterDeletionParams{
				ShortcutsCiphertext: shortcutsCiphertext,
				WidgetsCiphertext:   widgetsCiphertext,
				UpdatedAt:           pgTime(deletedAt),
				RegistrationID:      device.RegistrationID,
				ExpectedRevision:    device.Revision,
			}); err != nil {
			return err
		}
	}
	return nil
}

func (store *Store) resetDeviceWidgetSnapshots(
	ctx context.Context,
	queries *dbgen.Queries,
	viewID uuid.UUID,
	updatedAt time.Time,
) error {
	devices, err := queries.ListDeviceRegistrationsForUpdate(ctx)
	if err != nil {
		return err
	}
	for _, device := range devices {
		widgets := &deckv1.Device{}
		if err := store.openProto(
			"device-widgets", device.WidgetsCiphertext, widgets); err != nil {
			return err
		}
		changed := false
		for _, widget := range widgets.Widgets {
			if uuidValueFromProto(widget.GetViewId()) != viewID {
				continue
			}
			snapshot := widget.GetSnapshot()
			if snapshot != nil && snapshot.GetMatchingCount() == 0 &&
				len(snapshot.GetPullRequests()) == 0 &&
				snapshot.GetFreshness() ==
					deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED &&
				!snapshot.GetOffline() && snapshot.GetGeneratedAt() == nil {
				continue
			}
			widget.Snapshot = &deckv1.WidgetSnapshot{
				Freshness: deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED,
			}
			changed = true
		}
		if !changed {
			continue
		}
		widgetsCiphertext, err := store.sealProto("device-widgets", widgets)
		if err != nil {
			return err
		}
		if err := queries.UpdateDeviceWidgetsAfterViewChange(ctx,
			dbgen.UpdateDeviceWidgetsAfterViewChangeParams{
				WidgetsCiphertext: widgetsCiphertext,
				UpdatedAt:         pgTime(updatedAt),
				RegistrationID:    device.RegistrationID,
				ExpectedRevision:  device.Revision,
			}); err != nil {
			return err
		}
	}
	return nil
}

func retainShortcuts(
	shortcuts []*deckv1.ViewShortcut,
	deleted map[uuid.UUID]struct{},
) ([]*deckv1.ViewShortcut, bool) {
	remaining := make([]*deckv1.ViewShortcut, 0, len(shortcuts))
	for _, shortcut := range shortcuts {
		if _, remove := deleted[uuidValueFromProto(shortcut.GetViewId())]; remove {
			continue
		}
		remaining = append(remaining, shortcut)
	}
	return remaining, len(remaining) != len(shortcuts)
}

func retainWidgets(
	widgets []*deckv1.WidgetState,
	deleted map[uuid.UUID]struct{},
) ([]*deckv1.WidgetState, bool) {
	remaining := make([]*deckv1.WidgetState, 0, len(widgets))
	for _, widget := range widgets {
		if _, remove := deleted[uuidValueFromProto(widget.GetViewId())]; remove {
			continue
		}
		remaining = append(remaining, widget)
	}
	return remaining, len(remaining) != len(widgets)
}

func recalculateShortcutStates(shortcuts []*deckv1.ViewShortcut) error {
	counts := make(map[string]int, len(shortcuts))
	keys := make([]string, len(shortcuts))
	for index, shortcut := range shortcuts {
		key, err := shortcutbinding.CanonicalBinding(shortcut.GetBinding())
		if err != nil {
			return err
		}
		keys[index] = key
		counts[keys[index]]++
	}
	for index, shortcut := range shortcuts {
		shortcut.State = deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE
		if counts[keys[index]] > 1 {
			shortcut.State = deckv1.ShortcutState_SHORTCUT_STATE_CONFLICTED
		}
	}
	return nil
}
