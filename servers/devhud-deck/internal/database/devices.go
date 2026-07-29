package database

import (
	"bytes"
	"context"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type DeviceWrite struct {
	Platform                        deckv1.DevicePlatform
	DisplayName                     string
	Push                            *deckv1.PushRegistration
	DetailedNotificationTextEnabled bool
	Shortcuts                       []*deckv1.ViewShortcut
	Widgets                         []*deckv1.WidgetState
}

type RegisterDeviceParams struct {
	RegistrationID uuid.UUID
	DeviceID       uuid.UUID
	AccountID      uuid.UUID
	IdempotencyKey uuid.UUID
	RequestDigest  [32]byte
	OwnerHash      [32]byte
	Write          DeviceWrite
	Expected       uint64
	HasExpected    bool
	Grant          string
	LeaseExpiresAt time.Time
	Now            time.Time
}

func (store *Store) RegisterDevice(
	ctx context.Context,
	params RegisterDeviceParams,
) (*deckv1.DeviceRegistration, string, bool, error) {
	encoded, err := store.encodeDeviceWrite(params.Write)
	if err != nil {
		return nil, "", false, err
	}
	grantVerifier := security.GrantVerifier(params.Grant)
	grantReplay, err := store.cipher.Seal("grant-replay", []byte(params.Grant))
	if err != nil {
		return nil, "", false, err
	}
	var stored dbgen.DeckDeviceRegistration
	var registration *deckv1.DeviceRegistration
	var replayGrant string
	replayed := false
	err = store.withinTransaction(ctx, func(queries *dbgen.Queries) error {
		replay, replayErr := queries.GetRegisterDeviceIdempotency(ctx,
			dbgen.GetRegisterDeviceIdempotencyParams{
				AccountID: pgUUID(params.AccountID), IdempotencyKey: pgUUID(params.IdempotencyKey),
			})
		if replayErr == nil {
			if !bytes.Equal(replay.RequestDigest, params.RequestDigest[:]) {
				return ErrIdempotencyConflict
			}
			if !replay.LeaseExpiresAt.Valid || !replay.LeaseExpiresAt.Time.After(params.Now) {
				return ErrNotFound
			}
			registration = &deckv1.DeviceRegistration{}
			if openErr := store.openProto(
				"device-registration-replay",
				replay.ResponseCiphertext,
				registration,
			); openErr != nil {
				return openErr
			}
			plaintext, openErr := store.cipher.Open("grant-replay", replay.GrantReplayCiphertext)
			if openErr != nil {
				return openErr
			}
			replayGrant = string(plaintext)
			replayed = true
			return nil
		}
		if !errors.Is(replayErr, pgx.ErrNoRows) {
			return replayErr
		}
		if err := queries.EnsureOwnerLock(ctx, params.OwnerHash[:]); err != nil {
			return err
		}
		if _, err := queries.LockOwner(ctx, params.OwnerHash[:]); err != nil {
			return err
		}
		tombstoned, err := queries.IsOwnerTombstoned(ctx, params.OwnerHash[:])
		if err != nil {
			return err
		}
		if tombstoned {
			return ErrDeletionInProgress
		}
		_ = queries.DeleteExpiredDeviceIdempotency(ctx, pgTime(params.Now))
		_ = queries.DeleteExpiredDeviceByID(ctx, dbgen.DeleteExpiredDeviceByIDParams{
			DeviceID: pgUUID(params.DeviceID), Now: pgTime(params.Now),
		})
		current, currentErr := queries.GetDeviceByID(ctx, pgUUID(params.DeviceID))
		switch {
		case currentErr == nil:
			if uuidValue(current.AccountID) != params.AccountID {
				return ErrAccountSwitch
			}
			if !params.HasExpected || uint64(current.Revision) != params.Expected {
				return &StaleError{
					ResourceID: params.DeviceID, Revision: uint64(current.Revision),
				}
			}
			stored, err = queries.RenewDevice(ctx, dbgen.RenewDeviceParams{
				Platform:                        int16(params.Write.Platform),
				DisplayNameCiphertext:           encoded.displayName,
				PushCiphertext:                  encoded.push,
				DetailedNotificationTextEnabled: params.Write.DetailedNotificationTextEnabled,
				ShortcutsCiphertext:             encoded.shortcuts,
				WidgetsCiphertext:               encoded.widgets,
				GrantVerifier:                   grantVerifier[:],
				LeaseExpiresAt:                  pgTime(params.LeaseExpiresAt),
				UpdatedAt:                       pgTime(params.Now),
				RegistrationID:                  current.RegistrationID,
				ExpectedRevision:                int64(params.Expected),
			})
		case errors.Is(currentErr, pgx.ErrNoRows):
			if params.HasExpected {
				return &StaleError{ResourceID: params.DeviceID}
			}
			stored, err = queries.InsertDevice(ctx, dbgen.InsertDeviceParams{
				RegistrationID:                  pgUUID(params.RegistrationID),
				DeviceID:                        pgUUID(params.DeviceID),
				AccountID:                       pgUUID(params.AccountID),
				Platform:                        int16(params.Write.Platform),
				DisplayNameCiphertext:           encoded.displayName,
				PushCiphertext:                  encoded.push,
				DetailedNotificationTextEnabled: params.Write.DetailedNotificationTextEnabled,
				ShortcutsCiphertext:             encoded.shortcuts,
				WidgetsCiphertext:               encoded.widgets,
				GrantVerifier:                   grantVerifier[:],
				LeaseExpiresAt:                  pgTime(params.LeaseExpiresAt),
				CreatedAt:                       pgTime(params.Now),
				UpdatedAt:                       pgTime(params.Now),
			})
		default:
			return currentErr
		}
		if err != nil {
			return err
		}
		registration, err = store.decodeDevice(stored)
		if err != nil {
			return err
		}
		responseCiphertext, err := store.sealProto(
			"device-registration-replay", registration)
		if err != nil {
			return err
		}
		return queries.InsertRegisterDeviceIdempotency(ctx,
			dbgen.InsertRegisterDeviceIdempotencyParams{
				AccountID:             pgUUID(params.AccountID),
				IdempotencyKey:        pgUUID(params.IdempotencyKey),
				RequestDigest:         params.RequestDigest[:],
				RegistrationID:        stored.RegistrationID,
				GrantReplayCiphertext: grantReplay,
				GrantVerifier:         grantVerifier[:],
				ResponseCiphertext:    responseCiphertext,
				LeaseExpiresAt:        pgTime(params.LeaseExpiresAt),
			})
	})
	if err != nil {
		return nil, "", false, err
	}
	if replayed {
		params.Grant = replayGrant
	}
	return registration, params.Grant, replayed, nil
}

func (store *Store) GetDevice(
	ctx context.Context,
	accountID, deviceID uuid.UUID,
	now time.Time,
) (*deckv1.DeviceRegistration, error) {
	row, err := store.queries.GetDeviceByAccountAndID(ctx,
		dbgen.GetDeviceByAccountAndIDParams{
			AccountID: pgUUID(accountID), DeviceID: pgUUID(deviceID),
		})
	if errors.Is(err, pgx.ErrNoRows) || (err == nil &&
		(!row.LeaseExpiresAt.Valid || !row.LeaseExpiresAt.Time.After(now))) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("deck database: device lookup failed")
	}
	return store.decodeDevice(row)
}

func (store *Store) GetDeviceByRegistration(
	ctx context.Context,
	registrationID uuid.UUID,
) (*deckv1.DeviceRegistration, uuid.UUID, error) {
	row, err := store.queries.GetDeviceByRegistration(ctx, pgUUID(registrationID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, uuid.Nil, ErrNotFound
	}
	if err != nil {
		return nil, uuid.Nil, errors.New("deck database: registration lookup failed")
	}
	registration, err := store.decodeDevice(row)
	return registration, uuidValue(row.AccountID), err
}

func (store *Store) UpdateDevice(
	ctx context.Context,
	registrationID, accountID uuid.UUID,
	expected uint64,
	write DeviceWrite,
	now time.Time,
) (*deckv1.DeviceRegistration, error) {
	encoded, err := store.encodeDeviceWrite(write)
	if err != nil {
		return nil, err
	}
	row, err := store.queries.UpdateDevice(ctx, dbgen.UpdateDeviceParams{
		DisplayNameCiphertext:           encoded.displayName,
		PushCiphertext:                  encoded.push,
		DetailedNotificationTextEnabled: write.DetailedNotificationTextEnabled,
		ShortcutsCiphertext:             encoded.shortcuts,
		WidgetsCiphertext:               encoded.widgets,
		UpdatedAt:                       pgTime(now),
		RegistrationID:                  pgUUID(registrationID),
		AccountID:                       pgUUID(accountID),
		ExpectedRevision:                int64(expected),
	})
	if errors.Is(err, pgx.ErrNoRows) {
		current, currentErr := store.queries.GetDeviceByRegistration(ctx, pgUUID(registrationID))
		if errors.Is(currentErr, pgx.ErrNoRows) ||
			(currentErr == nil && uuidValue(current.AccountID) != accountID) {
			return nil, ErrNotFound
		}
		if currentErr != nil {
			return nil, errors.New("deck database: stale device lookup failed")
		}
		return nil, &StaleError{
			ResourceID: uuidValue(current.DeviceID), Revision: uint64(current.Revision),
		}
	}
	if err != nil {
		return nil, errors.New("deck database: update device failed")
	}
	return store.decodeDevice(row)
}

func (store *Store) UnregisterDevice(
	ctx context.Context,
	registrationID uuid.UUID,
	accountID uuid.UUID,
	grant string,
	now time.Time,
) (bool, error) {
	var count int64
	var err error
	if grant != "" {
		verifier := security.GrantVerifier(grant)
		count, err = store.queries.DeleteDeviceByRegistrationAndGrant(ctx,
			dbgen.DeleteDeviceByRegistrationAndGrantParams{
				RegistrationID: pgUUID(registrationID),
				GrantVerifier:  verifier[:],
				Now:            pgTime(now),
			})
	} else {
		count, err = store.queries.DeleteDeviceByRegistrationAndAccount(ctx,
			dbgen.DeleteDeviceByRegistrationAndAccountParams{
				RegistrationID: pgUUID(registrationID),
				AccountID:      pgUUID(accountID),
			})
	}
	if err != nil {
		return false, errors.New("deck database: unregister failed")
	}
	return count > 0, nil
}

func (store *Store) UpdateNotificationPreference(
	ctx context.Context,
	registrationID, viewID uuid.UUID,
	expected uint64,
	preference *deckv1.ViewNotificationPreference,
	now time.Time,
) (*deckv1.ViewNotificationState, error) {
	ciphertext, err := store.sealProto("device-notification", preference)
	if err != nil {
		return nil, err
	}
	row, err := store.queries.UpsertViewNotificationPreference(ctx,
		dbgen.UpsertViewNotificationPreferenceParams{
			RegistrationID:       pgUUID(registrationID),
			ViewID:               pgUUID(viewID),
			PreferenceCiphertext: ciphertext,
			UpdatedAt:            pgTime(now),
			ExpectedRevision:     int64(expected),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		current, currentErr := store.queries.GetViewNotificationPreference(ctx,
			dbgen.GetViewNotificationPreferenceParams{
				RegistrationID: pgUUID(registrationID), ViewID: pgUUID(viewID),
			})
		if errors.Is(currentErr, pgx.ErrNoRows) {
			return nil, &StaleError{ResourceID: viewID}
		}
		if currentErr != nil {
			return nil, errors.New("deck database: notification stale lookup failed")
		}
		return nil, &StaleError{ResourceID: viewID, Revision: uint64(current.Revision)}
	}
	if err != nil {
		return nil, errors.New("deck database: notification update failed")
	}
	return &deckv1.ViewNotificationState{
		RegistrationId: uuidProto(registrationID),
		ViewId:         uuidProto(viewID),
		Preference:     preference,
		Revision:       revisionProto(store.hasher, viewID, uint64(row.Revision)),
		UpdatedAt:      timestamppb.New(row.UpdatedAt.Time.UTC()),
	}, nil
}

type encodedDevice struct {
	displayName []byte
	push        []byte
	shortcuts   []byte
	widgets     []byte
}

func (store *Store) encodeDeviceWrite(write DeviceWrite) (encodedDevice, error) {
	displayName, err := store.cipher.Seal("device-display", []byte(write.DisplayName))
	if err != nil {
		return encodedDevice{}, err
	}
	push := write.Push
	if push == nil {
		push = &deckv1.PushRegistration{}
	}
	pushCiphertext, err := store.sealProto("device-push", push)
	if err != nil {
		return encodedDevice{}, err
	}
	shortcuts, err := store.sealProto("device-shortcuts",
		&deckv1.Device{Shortcuts: write.Shortcuts})
	if err != nil {
		return encodedDevice{}, err
	}
	widgets, err := store.sealProto("device-widgets",
		&deckv1.Device{Widgets: write.Widgets})
	if err != nil {
		return encodedDevice{}, err
	}
	return encodedDevice{
		displayName: displayName, push: pushCiphertext,
		shortcuts: shortcuts, widgets: widgets,
	}, nil
}

func (store *Store) decodeDevice(
	row dbgen.DeckDeviceRegistration,
) (*deckv1.DeviceRegistration, error) {
	displayName, err := store.cipher.Open("device-display", row.DisplayNameCiphertext)
	if err != nil {
		return nil, err
	}
	shortcuts := &deckv1.Device{}
	if err := store.openProto("device-shortcuts", row.ShortcutsCiphertext, shortcuts); err != nil {
		return nil, err
	}
	widgets := &deckv1.Device{}
	if err := store.openProto("device-widgets", row.WidgetsCiphertext, widgets); err != nil {
		return nil, err
	}
	deviceID := uuidValue(row.DeviceID)
	deviceRevision := uint64(row.Revision)
	for _, shortcut := range shortcuts.Shortcuts {
		if shortcutID := uuidValueFromProto(shortcut.GetShortcutId()); shortcutID != uuid.Nil {
			shortcut.Revision = revisionProto(store.hasher, shortcutID, deviceRevision)
		}
	}
	for _, widget := range widgets.Widgets {
		if widgetID := uuidValueFromProto(widget.GetWidgetId()); widgetID != uuid.Nil {
			widget.Revision = revisionProto(store.hasher, widgetID, deviceRevision)
		}
	}
	return &deckv1.DeviceRegistration{
		RegistrationId: uuidProto(uuidValue(row.RegistrationID)),
		Device: &deckv1.Device{
			DeviceId:                        uuidProto(deviceID),
			Platform:                        deckv1.DevicePlatform(row.Platform),
			DisplayName:                     string(displayName),
			DetailedNotificationTextEnabled: row.DetailedNotificationTextEnabled,
			Shortcuts:                       shortcuts.Shortcuts,
			Widgets:                         widgets.Widgets,
			Revision:                        revisionProto(store.hasher, deviceID, deviceRevision),
			CreatedAt:                       timestamppb.New(row.CreatedAt.Time.UTC()),
			UpdatedAt:                       timestamppb.New(row.UpdatedAt.Time.UTC()),
		},
		LeaseExpiresAt: timestamppb.New(row.LeaseExpiresAt.Time.UTC()),
	}, nil
}

func uuidValueFromProto(value *deckv1.UuidV7) uuid.UUID {
	if value == nil {
		return uuid.Nil
	}
	id, err := uuid.Parse(value.Value)
	if err != nil {
		return uuid.Nil
	}
	return id
}
