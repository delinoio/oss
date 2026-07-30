package database

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/contracts"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
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
	ViewRevision       uint64
	ViewerHash         [32]byte
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
	ViewRevision   uint64
	ViewerHash     [32]byte
	Origin         deckv1.RefreshOrigin
	ClientKind     deckv1.RefreshClientKind
	OrganizationID uuid.UUID
	TeamID         uuid.UUID
	Meter          contracts.RefreshMeter
	Now            time.Time
}

const (
	manualRefreshLimit  = 12
	manualRefreshWindow = time.Minute
)

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

// RefreshPersistence binds every derived refresh write to the transaction that
// holds the view revision fence.
type RefreshPersistence struct {
	store       *Store
	transaction pgx.Tx
	queries     *dbgen.Queries
}

func (store *Store) CheckViewRevision(
	ctx context.Context,
	viewID uuid.UUID,
	expectedRevision uint64,
) error {
	var revision int64
	err := store.pool.QueryRow(
		ctx, "SELECT revision FROM deck_views WHERE view_id = $1", viewID,
	).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("deck database: view revision lookup failed")
	}
	if revision < 1 || uint64(revision) != expectedRevision {
		return &StaleError{ResourceID: viewID, Revision: uint64(revision)}
	}
	return nil
}

func (persistence *RefreshPersistence) ReplaceSnapshots(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	snapshots []*deckv1.PullRequestResult,
	refreshedAt time.Time,
) (bool, error) {
	return persistence.store.replaceSnapshots(
		ctx, persistence.queries, viewID, viewerHash, snapshots, refreshedAt)
}

func (persistence *RefreshPersistence) ActiveNotificationPreferences(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	now time.Time,
) ([]NotificationPreferenceRecord, error) {
	return persistence.store.activeNotificationPreferences(
		ctx, persistence.transaction, accountID, viewID, now)
}

func (persistence *RefreshPersistence) CreateNotificationEvents(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	events []NotificationEventWrite,
	now time.Time,
) error {
	return persistence.store.createNotificationEvents(
		ctx, persistence.transaction, viewID, viewerHash, events, now)
}

func (persistence *RefreshPersistence) UpdateWidgetSnapshots(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	snapshots []*deckv1.PullRequestResult,
	truncated bool,
	refreshedAt time.Time,
	updatedAt time.Time,
) error {
	return persistence.store.updateWidgetSnapshots(
		ctx, persistence.transaction, accountID, viewID,
		snapshots, truncated, refreshedAt, updatedAt)
}

func (persistence *RefreshPersistence) SaveRefreshPendingResponse(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	response *deckv1.RefreshViewResponse,
	now time.Time,
) error {
	return persistence.store.saveRefreshPendingResponse(
		ctx, persistence.transaction, subjectHash, requestID, response, now)
}

// WithViewRevisionLock fences refresh persistence against concurrent view
// updates and commits all callback writes atomically with the revision check.
func (store *Store) WithViewRevisionLock(
	ctx context.Context,
	viewID uuid.UUID,
	expectedRevision uint64,
	callback func(*RefreshPersistence) error,
) error {
	if store == nil || store.pool == nil || callback == nil {
		return errors.New("deck database: view revision lock unavailable")
	}
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return errors.New("deck database: view revision lock unavailable")
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	var revision int64
	err = transaction.QueryRow(ctx, `
		SELECT revision
		FROM deck_views
		WHERE view_id = $1
		FOR NO KEY UPDATE
	`, viewID).Scan(&revision)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return errors.New("deck database: view revision lock unavailable")
	}
	if revision < 1 || uint64(revision) != expectedRevision {
		return &StaleError{ResourceID: viewID, Revision: uint64(revision)}
	}
	persistence := &RefreshPersistence{
		store:       store,
		transaction: transaction,
		queries:     store.queries.WithTx(transaction),
	}
	if err := callback(persistence); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return errors.New("deck database: view revision lock unavailable")
	}
	return nil
}

const insertRefreshAttemptSQL = `
	INSERT INTO deck_refresh_attempts (
		subject_hash, refresh_request_id, request_digest, view_id,
		view_revision, viewer_hash, origin, client_kind,
		billing_organization_id, billing_team_id, meter_id, price_version_id,
		service_identity_id, usd_micros, state, created_at, updated_at
	) VALUES (
		$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
		$15, $16, $16
	)
	ON CONFLICT (subject_hash, refresh_request_id) DO NOTHING
`

func (store *Store) BeginRefreshAttempt(
	ctx context.Context,
	params BeginRefreshAttemptParams,
) (RefreshAttempt, bool, error) {
	if params.RequestID.Version() != 7 || params.ViewID.Version() != 7 ||
		params.ViewRevision == 0 ||
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
	if params.Origin == deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL {
		return store.beginManualRefreshAttempt(ctx, params)
	}
	result, err := store.pool.Exec(ctx, insertRefreshAttemptSQL,
		params.SubjectHash[:], params.RequestID, params.RequestDigest[:],
		params.ViewID, int64(params.ViewRevision), params.ViewerHash[:],
		int16(params.Origin), int16(params.ClientKind), params.OrganizationID,
		params.TeamID, params.Meter.MeterID, params.Meter.PriceVersionID,
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

func (store *Store) beginManualRefreshAttempt(
	ctx context.Context,
	params BeginRefreshAttemptParams,
) (RefreshAttempt, bool, error) {
	transaction, err := store.pool.Begin(ctx)
	if err != nil {
		return RefreshAttempt{}, false,
			errors.New("deck database: refresh attempt insert failed")
	}
	defer func() { _ = transaction.Rollback(ctx) }()
	lockDigest := store.hasher.Sum(
		"manual-refresh-rate-lock", string(params.SubjectHash[:]))
	lockKey := int64(binary.BigEndian.Uint64(lockDigest[:8]))
	if _, err := transaction.Exec(
		ctx, "SELECT pg_advisory_xact_lock($1)", lockKey); err != nil {
		return RefreshAttempt{}, false,
			errors.New("deck database: refresh attempt insert failed")
	}
	var exists bool
	if err := transaction.QueryRow(ctx, `
		SELECT EXISTS (
			SELECT 1
			FROM deck_refresh_attempts
			WHERE subject_hash = $1 AND refresh_request_id = $2
		)
	`, params.SubjectHash[:], params.RequestID).Scan(&exists); err != nil {
		return RefreshAttempt{}, false,
			errors.New("deck database: refresh attempt lookup failed")
	}
	if !exists {
		var recent int
		if err := transaction.QueryRow(ctx, `
			SELECT count(*)::integer
			FROM deck_refresh_attempts
			WHERE subject_hash = $1 AND origin = $2 AND created_at > $3
		`, params.SubjectHash[:],
			int16(deckv1.RefreshOrigin_REFRESH_ORIGIN_MANUAL),
			params.Now.UTC().Add(-manualRefreshWindow)).Scan(&recent); err != nil {
			return RefreshAttempt{}, false,
				errors.New("deck database: refresh rate lookup failed")
		}
		if recent >= manualRefreshLimit {
			return RefreshAttempt{}, false, ErrRefreshRateLimited
		}
		if _, err := transaction.Exec(ctx, insertRefreshAttemptSQL,
			params.SubjectHash[:], params.RequestID, params.RequestDigest[:],
			params.ViewID, int64(params.ViewRevision), params.ViewerHash[:],
			int16(params.Origin), int16(params.ClientKind), params.OrganizationID,
			params.TeamID, params.Meter.MeterID, params.Meter.PriceVersionID,
			params.Meter.ServiceID, params.Meter.USDMicros,
			int16(RefreshAttemptCreated), params.Now.UTC()); err != nil {
			return RefreshAttempt{}, false,
				errors.New("deck database: refresh attempt insert failed")
		}
	}
	if err := transaction.Commit(ctx); err != nil {
		return RefreshAttempt{}, false,
			errors.New("deck database: refresh attempt insert failed")
	}
	attempt, err := store.GetRefreshAttempt(
		ctx, params.SubjectHash, params.RequestID, params.RequestDigest)
	return attempt, exists, err
}

func (store *Store) GetRefreshAttempt(
	ctx context.Context,
	subjectHash [32]byte,
	requestID uuid.UUID,
	requestDigest [32]byte,
) (RefreshAttempt, error) {
	var storedDigest []byte
	var viewID uuid.UUID
	var viewRevision int64
	var viewerHash []byte
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
		       price_version_id, service_identity_id, usd_micros,
		       view_revision, viewer_hash
		FROM deck_refresh_attempts
		WHERE subject_hash = $1 AND refresh_request_id = $2
	`, subjectHash[:], requestID).Scan(
		&storedDigest, &viewID, &state, &reservationID,
		&dispatched, &responseCiphertext, &organizationID, &teamID,
		&meterID, &priceVersionID, &serviceID, &usdMicros,
		&viewRevision, &viewerHash)
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
	if viewRevision < 1 || len(viewerHash) != 32 {
		return RefreshAttempt{}, errors.New(
			"deck database: invalid refresh attempt")
	}
	attempt := RefreshAttempt{
		RequestID: requestID, ViewID: viewID,
		ViewRevision: uint64(viewRevision),
		State:        RefreshAttemptState(state), ProviderDispatched: dispatched,
		OrganizationID: organizationID, TeamID: teamID,
		Meter: contracts.RefreshMeter{
			MeterID: meterID, PriceVersionID: priceVersionID,
			ServiceID: serviceID, USDMicros: usdMicros,
		},
	}
	copy(attempt.ViewerHash[:], viewerHash)
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
// an earlier dispatched attempt completed its billing accounting. The current
// request is excluded because its attempt row is inserted before this check.
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
			  AND provider_dispatched_at > $4
			  AND state = $5
		)
	`, viewID, viewerHash[:], currentRequestID, cutoff.UTC(),
		int16(RefreshAttemptCompleted)).Scan(&recent)
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
		SET state = $3, provider_dispatched = true,
		    provider_dispatched_at = COALESCE(provider_dispatched_at, $4),
		    updated_at = $4
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
	return store.saveRefreshPendingResponse(
		ctx, store.pool, subjectHash, requestID, response, now)
}

type refreshExecer interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
}

func (store *Store) saveRefreshPendingResponse(
	ctx context.Context,
	execer refreshExecer,
	subjectHash [32]byte,
	requestID uuid.UUID,
	response *deckv1.RefreshViewResponse,
	now time.Time,
) error {
	ciphertext, err := store.sealProto("refresh-response", response)
	if err != nil {
		return err
	}
	result, err := execer.Exec(ctx, `
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
			if activeShortcutTargetsView(shortcut, viewID) {
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

func activeShortcutTargetsView(
	shortcut *deckv1.ViewShortcut,
	viewID uuid.UUID,
) bool {
	return shortcut.GetState() ==
		deckv1.ShortcutState_SHORTCUT_STATE_ACTIVE &&
		shortcut.GetViewId().GetValue() == viewID.String()
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
