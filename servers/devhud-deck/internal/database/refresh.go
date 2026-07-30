package database

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type RefreshAttemptState int16

const (
	RefreshAttemptCreated RefreshAttemptState = iota + 1
	RefreshAttemptReserved
	RefreshAttemptDispatched
	RefreshAttemptCompleted
)

type RefreshAttempt struct {
	RequestID          uuid.UUID
	ViewID             uuid.UUID
	State              RefreshAttemptState
	ReservationID      uuid.UUID
	ProviderDispatched bool
	Response           *deckv1.RefreshViewResponse
	OrganizationID     uuid.UUID
	TeamID             uuid.UUID
	Meter              contracts.RefreshMeter
}

type BeginRefreshAttemptParams struct {
	SubjectHash    [32]byte
	RequestID      uuid.UUID
	RequestDigest  [32]byte
	ViewID         uuid.UUID
	ViewerHash     [32]byte
	Origin         deckv1.RefreshOrigin
	ClientKind     deckv1.RefreshClientKind
	OrganizationID uuid.UUID
	TeamID         uuid.UUID
	Meter          contracts.RefreshMeter
	Now            time.Time
}

// WithRefreshLock serializes the same viewer/view across every Deck process.
// The lock lives only for this active request and cannot enqueue later work.
func (store *Store) WithRefreshLock(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	callback func() error,
) error {
	if store == nil || store.pool == nil || callback == nil {
		return errors.New("deck database: refresh lock unavailable")
	}
	sum := store.hasher.Sum(
		"refresh-lock", viewID.String()+"\x00"+string(viewerHash[:]))
	key := int64(binary.BigEndian.Uint64(sum[:8]))
	connection, err := store.pool.Acquire(ctx)
	if err != nil {
		return errors.New("deck database: refresh lock unavailable")
	}
	defer connection.Release()
	transaction, err := connection.Begin(ctx)
	if err != nil {
		return errors.New("deck database: refresh lock unavailable")
	}
	if _, err := transaction.Exec(
		ctx, "SELECT pg_advisory_xact_lock($1)", key); err != nil {
		_ = transaction.Rollback(ctx)
		return errors.New("deck database: refresh lock unavailable")
	}
	if err := callback(); err != nil {
		_ = transaction.Rollback(ctx)
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("deck database: refresh lock unavailable")
	}
	return nil
}

func (store *Store) BeginRefreshAttempt(
	ctx context.Context,
	params BeginRefreshAttemptParams,
) (RefreshAttempt, bool, error) {
	if params.RequestID.Version() != 7 || params.ViewID.Version() != 7 ||
		params.OrganizationID.Version() != 7 || params.TeamID.Version() != 7 ||
		params.Meter.MeterID.Version() != 7 ||
		params.Meter.PriceVersionID.Version() != 7 ||
		params.Meter.ServiceID.Version() != 7 ||
		params.Meter.USDMicros != contracts.ProviderRefreshPriceUSDMicros ||
		params.Origin < deckv1.RefreshOrigin_REFRESH_ORIGIN_AUTOMATIC ||
		params.Origin > deckv1.RefreshOrigin_REFRESH_ORIGIN_SHORTCUT ||
		params.ClientKind < deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_DESKTOP ||
		params.ClientKind > deckv1.RefreshClientKind_REFRESH_CLIENT_KIND_WIDGET {
		return RefreshAttempt{}, false, errors.New(
			"deck database: invalid refresh attempt")
	}
	result, err := store.pool.Exec(ctx, `
		INSERT INTO deck_refresh_attempts (
			subject_hash, refresh_request_id, request_digest, view_id,
			viewer_hash, origin, client_kind, billing_organization_id,
			billing_team_id, meter_id, price_version_id, service_identity_id,
			usd_micros, state, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13,
			$14, $15, $15
		)
		ON CONFLICT (subject_hash, refresh_request_id) DO NOTHING
	`, params.SubjectHash[:], params.RequestID, params.RequestDigest[:],
		params.ViewID, params.ViewerHash[:], int16(params.Origin),
		int16(params.ClientKind), params.OrganizationID, params.TeamID,
		params.Meter.MeterID, params.Meter.PriceVersionID,
		params.Meter.ServiceID, params.Meter.USDMicros,
		int16(RefreshAttemptCreated), params.Now.UTC())
	if err != nil {
		return RefreshAttempt{}, false, errors.New(
			"deck database: refresh attempt insert failed")
	}
	attempt, err := store.GetRefreshAttempt(
		ctx, params.SubjectHash, params.RequestID, params.RequestDigest)
	return attempt, result.RowsAffected() == 0, err
}

func (store *Store) GetRefreshAttempt(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	requestDigest [32]byte,
) (RefreshAttempt, error) {
	var storedDigest []byte
	var viewID uuid.UUID
	var state int16
	var reservationID *uuid.UUID
	var dispatched bool
	var responseCiphertext []byte
	var organizationID, teamID uuid.UUID
	var meterID, priceVersionID, serviceID uuid.UUID
	var usdMicros int64
	err := store.pool.QueryRow(ctx, `
		SELECT request_digest, view_id, state, reservation_id,
		       provider_dispatched, response_ciphertext,
		       billing_organization_id, billing_team_id, meter_id,
		       price_version_id, service_identity_id, usd_micros
		FROM deck_refresh_attempts
		WHERE subject_hash = $1 AND refresh_request_id = $2
	`, subjectHash[:], requestID).Scan(
		&storedDigest, &viewID, &state, &reservationID,
		&dispatched, &responseCiphertext, &organizationID, &teamID,
		&meterID, &priceVersionID, &serviceID, &usdMicros)
	if errors.Is(err, pgx.ErrNoRows) {
		return RefreshAttempt{}, ErrNotFound
	}
	if err != nil {
		return RefreshAttempt{}, errors.New(
			"deck database: refresh attempt lookup failed")
	}
	if !bytes.Equal(storedDigest, requestDigest[:]) {
		return RefreshAttempt{}, ErrIdempotencyConflict
	}
	attempt := RefreshAttempt{
		RequestID: requestID, ViewID: viewID,
		State: RefreshAttemptState(state), ProviderDispatched: dispatched,
		OrganizationID: organizationID, TeamID: teamID,
		Meter: contracts.RefreshMeter{
			MeterID: meterID, PriceVersionID: priceVersionID,
			ServiceID: serviceID, USDMicros: usdMicros,
		},
	}
	if reservationID != nil {
		attempt.ReservationID = *reservationID
	}
	if len(responseCiphertext) > 0 {
		attempt.Response = &deckv1.RefreshViewResponse{}
		if err := store.openProto(
			"refresh-response", responseCiphertext, attempt.Response); err != nil {
			return RefreshAttempt{}, err
		}
	}
	return attempt, nil
}

// HasRecentAutomaticRefreshAttempt coalesces active client requests only after
// an earlier attempt actually dispatched to GitHub. The current request is
// excluded because its attempt row is inserted before this check.
func (store *Store) HasRecentAutomaticRefreshAttempt(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	currentRequestID uuid.UUID,
	cutoff time.Time,
) (bool, error) {
	var recent bool
	err := store.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM deck_refresh_attempts
			WHERE view_id = $1 AND viewer_hash = $2
			  AND refresh_request_id <> $3
			  AND origin IN (1, 2)
			  AND provider_dispatched
			  AND created_at > $4
		)
	`, viewID, viewerHash[:], currentRequestID, cutoff.UTC()).Scan(&recent)
	if err != nil {
		return false, errors.New(
			"deck database: refresh coalescing lookup failed")
	}
	return recent, nil
}

func (store *Store) MarkRefreshReserved(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	reservationID uuid.UUID,
	now time.Time,
) error {
	return store.advanceRefresh(ctx, subjectHash, requestID, `
		UPDATE deck_refresh_attempts
		SET state = $3, reservation_id = $4, updated_at = $5
		WHERE subject_hash = $1 AND refresh_request_id = $2
		  AND state IN (1, 2)
	`, int16(RefreshAttemptReserved), reservationID, now.UTC())
}

func (store *Store) MarkRefreshDispatched(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	now time.Time,
) error {
	result, err := store.pool.Exec(ctx, `
		UPDATE deck_refresh_attempts
		SET state = $3, provider_dispatched = true, updated_at = $4
		WHERE subject_hash = $1 AND refresh_request_id = $2
		  AND state IN (2, 3)
	`, subjectHash[:], requestID, int16(RefreshAttemptDispatched), now.UTC())
	if err != nil || result.RowsAffected() != 1 {
		return errors.New("deck database: refresh attempt update failed")
	}
	return nil
}

func (store *Store) SaveRefreshResponse(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	response *deckv1.RefreshViewResponse,
	completed bool,
	now time.Time,
) error {
	ciphertext, err := store.sealProto("refresh-response", response)
	if err != nil {
		return err
	}
	state := RefreshAttemptDispatched
	if completed {
		state = RefreshAttemptCompleted
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE deck_refresh_attempts
		SET state = $3, response_ciphertext = $4, updated_at = $5
		WHERE subject_hash = $1 AND refresh_request_id = $2
		  AND state <> 4
	`, subjectHash[:], requestID, int16(state), ciphertext, now.UTC())
	if err != nil || result.RowsAffected() != 1 {
		return errors.New("deck database: refresh response update failed")
	}
	return nil
}

// SaveRefreshPendingResponse records the terminal provider outcome before the
// live reservation is finalized. It deliberately preserves the reserved or
// dispatched state so an authenticated client retry knows whether to release
// or commit without issuing another provider request.
func (store *Store) SaveRefreshPendingResponse(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	response *deckv1.RefreshViewResponse,
	now time.Time,
) error {
	ciphertext, err := store.sealProto("refresh-response", response)
	if err != nil {
		return err
	}
	result, err := store.pool.Exec(ctx, `
		UPDATE deck_refresh_attempts
		SET response_ciphertext = $3, updated_at = $4
		WHERE subject_hash = $1 AND refresh_request_id = $2
		  AND state IN (2, 3)
	`, subjectHash[:], requestID, ciphertext, now.UTC())
	if err != nil || result.RowsAffected() != 1 {
		return errors.New("deck database: refresh response update failed")
	}
	return nil
}

func (store *Store) advanceRefresh(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	statement string,
	state int16,
	reservationID uuid.UUID,
	now time.Time,
) error {
	var reservation any
	if reservationID != uuid.Nil {
		reservation = reservationID
	}
	result, err := store.pool.Exec(
		ctx, statement, subjectHash[:], requestID, state, reservation, now)
	if err != nil || result.RowsAffected() != 1 {
		return errors.New("deck database: refresh attempt update failed")
	}
	return nil
}

func (store *Store) TouchViewOpened(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	now time.Time,
) error {
	_, err := store.pool.Exec(ctx, `
		INSERT INTO deck_view_viewer_activity (
			view_id, viewer_hash, last_opened_at
		) VALUES ($1, $2, $3)
		ON CONFLICT (view_id, viewer_hash) DO UPDATE
		SET last_opened_at = GREATEST(
			deck_view_viewer_activity.last_opened_at,
			EXCLUDED.last_opened_at
		)
	`, viewID, viewerHash[:], now.UTC())
	if err != nil {
		return errors.New("deck database: view activity update failed")
	}
	return nil
}

func (store *Store) ViewOpenedSince(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	cutoff time.Time,
) (bool, error) {
	var eligible bool
	err := store.pool.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM deck_view_viewer_activity
			WHERE view_id = $1 AND viewer_hash = $2
			  AND last_opened_at >= $3
		)
	`, viewID, viewerHash[:], cutoff.UTC()).Scan(&eligible)
	if err != nil {
		return false, errors.New("deck database: view activity lookup failed")
	}
	return eligible, nil
}

func (store *Store) HasActiveViewDeviceAttachment(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	now time.Time,
) (bool, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT shortcuts_ciphertext, widgets_ciphertext
		FROM deck_device_registrations
		WHERE account_id = $1 AND lease_expires_at > $2
	`, accountID, now.UTC())
	if err != nil {
		return false, errors.New("deck database: device attachment lookup failed")
	}
	defer rows.Close()
	for rows.Next() {
		var shortcutsCiphertext, widgetsCiphertext []byte
		if err := rows.Scan(&shortcutsCiphertext, &widgetsCiphertext); err != nil {
			return false, errors.New("deck database: device attachment lookup failed")
		}
		shortcuts := &deckv1.Device{}
		widgets := &deckv1.Device{}
		if err := store.openProto(
			"device-shortcuts", shortcutsCiphertext, shortcuts); err != nil {
			return false, err
		}
		if err := store.openProto(
			"device-widgets", widgetsCiphertext, widgets); err != nil {
			return false, err
		}
		for _, shortcut := range shortcuts.GetShortcuts() {
			if shortcut.GetViewId().GetValue() == viewID.String() {
				return true, nil
			}
		}
		for _, widget := range widgets.GetWidgets() {
			if widget.GetViewId().GetValue() == viewID.String() {
				return true, nil
			}
		}
	}
	if err := rows.Err(); err != nil {
		return false, errors.New("deck database: device attachment lookup failed")
	}
	return false, nil
}

func (store *Store) ListAllSnapshots(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
) ([]*deckv1.PullRequestResult, bool, time.Time, error) {
	hashes, err := store.ListSnapshotRepositoryHashes(ctx, viewID, viewerHash)
	if err != nil {
		return nil, false, time.Time{}, err
	}
	readable := make(map[[32]byte]struct{}, len(hashes))
	for _, hash := range hashes {
		readable[hash] = struct{}{}
	}
	return store.ListSnapshots(ctx, viewID, viewerHash, readable)
}

func freshnessState(now, refreshedAt time.Time) deckv1.FreshnessState {
	switch {
	case refreshedAt.IsZero():
		return deckv1.FreshnessState_FRESHNESS_STATE_NEVER_REFRESHED
	case !now.Before(refreshedAt.Add(5 * time.Minute)):
		return deckv1.FreshnessState_FRESHNESS_STATE_STALE
	default:
		return deckv1.FreshnessState_FRESHNESS_STATE_FRESH
	}
}

func applyWidgetFreshness(
	registration *deckv1.DeviceRegistration,
	now time.Time,
) {
	if registration == nil || registration.Device == nil {
		return
	}
	for _, widget := range registration.Device.Widgets {
		snapshot := widget.GetSnapshot()
		if snapshot == nil || snapshot.GetGeneratedAt() == nil {
			continue
		}
		snapshot.Freshness = freshnessState(
			now.UTC(), snapshot.GetGeneratedAt().AsTime().UTC())
	}
}

func notificationDetail(
	snapshot *deckv1.PullRequestResult,
) *deckv1.PullRequestDetail {
	if snapshot == nil {
		return nil
	}
	return &deckv1.PullRequestDetail{
		Result:                snapshot,
		SupportedMutations:    snapshot.SupportedMutations,
		AvailableMergeMethods: snapshot.AvailableMergeMethods,
		Revision:              snapshot.Revision,
	}
}
