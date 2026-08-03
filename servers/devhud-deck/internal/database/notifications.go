package database

import (
	"context"
	"errors"
	"strings"
	"time"

	deckv1 "github.com/delinoio/oss/protos/devhud-deck/gen/go/devhud-deck/v1"
	"github.com/delinoio/oss/servers/devhud-deck/internal/database/dbgen"
	"github.com/delinoio/oss/servers/devhud-deck/internal/security"
	"github.com/delinoio/oss/servers/internal/uuidv7"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	notificationRetention          = 30 * 24 * time.Hour
	widgetSnapshotPullRequestLimit = 10
)

type NotificationEventWrite struct {
	RegistrationID uuid.UUID
	Transition     deckv1.NotificationTransition
	Snapshot       *deckv1.PullRequestResult
}

type NotificationPreferenceRecord struct {
	RegistrationID uuid.UUID
	Preference     *deckv1.ViewNotificationPreference
}

type NotificationEventRecord struct {
	EventID           uuid.UUID
	ViewID            uuid.UUID
	RegistrationID    uuid.UUID
	ViewerHash        [32]byte
	RepositoryHash    [32]byte
	Transition        deckv1.NotificationTransition
	PullRequestNumber uint64
	CreatedAt         time.Time
	ExpiresAt         time.Time
}

type refreshSQLExecutor interface {
	Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func (store *Store) CreateNotificationEvents(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	events []NotificationEventWrite,
	now time.Time,
) error {
	return store.createNotificationEvents(
		ctx, store.pool, viewID, viewerHash, events, now)
}

func (store *Store) createNotificationEvents(
	ctx context.Context,
	executor refreshSQLExecutor,
	viewID uuid.UUID,
	viewerHash [32]byte,
	events []NotificationEventWrite,
	now time.Time,
) error {
	if err := store.pruneNotificationHistory(ctx, executor, now); err != nil {
		return err
	}
	for _, event := range events {
		repository := event.Snapshot.GetRepository()
		if event.RegistrationID.Version() != 7 ||
			event.Transition <
				deckv1.NotificationTransition_NOTIFICATION_TRANSITION_ASSIGNED ||
			event.Transition >
				deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CLOSED ||
			repository == nil || event.Snapshot.GetNumber() == 0 {
			return errors.New("deck database: invalid notification transition")
		}
		eventID, err := uuidv7.New()
		if err != nil {
			return err
		}
		opaque, err := security.NewGrant()
		if err != nil {
			return err
		}
		verifier := security.GrantVerifier(opaque)
		detail := notificationDetail(event.Snapshot)
		ciphertext, err := store.sealProto("notification-detail", detail)
		if err != nil {
			return err
		}
		repositoryHash := store.SnapshotRepositoryHash(repository)
		result, err := executor.Exec(ctx, `
			INSERT INTO deck_notification_events (
				event_id, view_id, registration_id, opaque_event_id, transition,
				created_at, expires_at, viewer_hash, repository_hash,
				pull_request_number, detail_ciphertext
			)
			SELECT $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
			FROM deck_device_registrations
			WHERE registration_id = $3 AND lease_expires_at > $6
		`, eventID, viewID, event.RegistrationID, verifier[:],
			int16(event.Transition), now.UTC(),
			now.UTC().Add(notificationRetention), viewerHash[:],
			repositoryHash[:], int64(event.Snapshot.GetNumber()),
			ciphertext)
		if err != nil {
			return errors.New("deck database: notification insert failed")
		}
		// An unregister or lease expiry racing this refresh removes the target
		// without turning optional notification delivery into a refresh failure.
		if result.RowsAffected() == 0 {
			continue
		}
	}
	return nil
}

func (store *Store) PruneNotificationHistory(
	ctx context.Context,
	now time.Time,
) error {
	return store.pruneNotificationHistory(ctx, store.pool, now)
}

func (store *Store) pruneNotificationHistory(
	ctx context.Context,
	executor refreshSQLExecutor,
	now time.Time,
) error {
	if _, err := executor.Exec(ctx,
		"DELETE FROM deck_notification_events WHERE expires_at <= $1",
		now.UTC()); err != nil {
		return errors.New("deck database: notification retention failed")
	}
	return nil
}

func (store *Store) GetNotificationEventMetadata(
	ctx context.Context,
	opaque string,
	now time.Time,
) (NotificationEventRecord, error) {
	if opaque == "" {
		return NotificationEventRecord{}, ErrNotFound
	}
	verifier := security.GrantVerifier(opaque)
	var record NotificationEventRecord
	var viewerHash []byte
	var repositoryHash []byte
	var transition int16
	var pullRequestNumber int64
	err := store.pool.QueryRow(ctx, `
		SELECT event_id, view_id, registration_id, viewer_hash, transition,
		       repository_hash, pull_request_number, created_at, expires_at
		FROM deck_notification_events
		WHERE opaque_event_id = $1 AND expires_at > $2
		  AND registration_id IS NOT NULL
		  AND viewer_hash IS NOT NULL
		  AND detail_ciphertext IS NOT NULL
	`, verifier[:], now.UTC()).Scan(
		&record.EventID, &record.ViewID, &record.RegistrationID,
		&viewerHash, &transition,
		&repositoryHash, &pullRequestNumber,
		&record.CreatedAt, &record.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationEventRecord{}, ErrNotFound
	}
	if err != nil || record.EventID.Version() != 7 ||
		record.ViewID.Version() != 7 ||
		record.RegistrationID.Version() != 7 || len(viewerHash) != 32 ||
		len(repositoryHash) != 32 ||
		pullRequestNumber <= 0 ||
		transition <
			int16(deckv1.NotificationTransition_NOTIFICATION_TRANSITION_ASSIGNED) ||
		transition >
			int16(deckv1.NotificationTransition_NOTIFICATION_TRANSITION_CLOSED) {
		return NotificationEventRecord{}, errors.New(
			"deck database: notification lookup failed")
	}
	copy(record.ViewerHash[:], viewerHash)
	copy(record.RepositoryHash[:], repositoryHash)
	record.Transition = deckv1.NotificationTransition(transition)
	record.PullRequestNumber = uint64(pullRequestNumber)
	return record, nil
}

func (store *Store) GetNotificationEventDetail(
	ctx context.Context,
	event NotificationEventRecord,
	now time.Time,
) (*deckv1.PullRequestDetail, error) {
	if event.EventID.Version() != 7 || event.ViewID.Version() != 7 ||
		event.RegistrationID.Version() != 7 ||
		event.PullRequestNumber == 0 {
		return nil, ErrNotFound
	}
	var ciphertext []byte
	err := store.pool.QueryRow(ctx, `
		SELECT detail_ciphertext
		FROM deck_notification_events
		WHERE event_id = $1 AND view_id = $2 AND viewer_hash = $3
		  AND registration_id = $4
		  AND transition = $5 AND pull_request_number = $6
		  AND repository_hash = $7
		  AND expires_at > $8 AND detail_ciphertext IS NOT NULL
	`, event.EventID, event.ViewID, event.ViewerHash[:],
		event.RegistrationID, int16(event.Transition),
		int64(event.PullRequestNumber), event.RepositoryHash[:],
		now.UTC()).Scan(&ciphertext)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("deck database: notification lookup failed")
	}
	detail := &deckv1.PullRequestDetail{}
	if err := store.openProto(
		"notification-detail", ciphertext, detail); err != nil {
		return nil, err
	}
	repository := detail.GetResult().GetRepository()
	if repository == nil ||
		store.SnapshotRepositoryHash(repository) != event.RepositoryHash ||
		detail.GetResult().GetNumber() != event.PullRequestNumber {
		return nil, errors.New(
			"deck database: invalid notification record")
	}
	return detail, nil
}

func (store *Store) NotificationPreference(
	ctx context.Context,
	registrationID uuid.UUID,
	viewID uuid.UUID,
) (*deckv1.ViewNotificationPreference, error) {
	row, err := store.queries.GetViewNotificationPreference(
		ctx, dbgen.GetViewNotificationPreferenceParams{
			RegistrationID: pgUUID(registrationID), ViewID: pgUUID(viewID),
		})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, errors.New("deck database: notification preference lookup failed")
	}
	preference := &deckv1.ViewNotificationPreference{}
	if err := store.openProto(
		"device-notification", row.PreferenceCiphertext, preference); err != nil {
		return nil, err
	}
	return preference, nil
}

func (store *Store) ActiveNotificationPreferences(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	now time.Time,
) ([]NotificationPreferenceRecord, error) {
	return store.activeNotificationPreferences(
		ctx, store.pool, accountID, viewID, now)
}

func (store *Store) activeNotificationPreferences(
	ctx context.Context,
	executor refreshSQLExecutor,
	accountID uuid.UUID,
	viewID uuid.UUID,
	now time.Time,
) ([]NotificationPreferenceRecord, error) {
	rows, err := executor.Query(ctx, `
		SELECT preference.registration_id, preference.preference_ciphertext
		FROM deck_view_notification_preferences AS preference
		JOIN deck_device_registrations AS registration
		  ON registration.registration_id = preference.registration_id
		WHERE registration.account_id = $1
		  AND preference.view_id = $2
		  AND registration.lease_expires_at > $3
	`, accountID, viewID, now.UTC())
	if err != nil {
		return nil, errors.New(
			"deck database: notification preference lookup failed")
	}
	defer rows.Close()
	var preferences []NotificationPreferenceRecord
	for rows.Next() {
		var registrationID uuid.UUID
		var ciphertext []byte
		if err := rows.Scan(&registrationID, &ciphertext); err != nil {
			return nil, errors.New(
				"deck database: notification preference lookup failed")
		}
		preference := &deckv1.ViewNotificationPreference{}
		if err := store.openProto(
			"device-notification", ciphertext, preference); err != nil {
			return nil, err
		}
		if registrationID.Version() != 7 {
			return nil, errors.New(
				"deck database: notification preference lookup failed")
		}
		preferences = append(preferences, NotificationPreferenceRecord{
			RegistrationID: registrationID, Preference: preference,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, errors.New(
			"deck database: notification preference lookup failed")
	}
	return preferences, nil
}

func (store *Store) UpdateWidgetSnapshots(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	snapshots []*deckv1.PullRequestResult,
	truncated bool,
	refreshedAt time.Time,
	updatedAt time.Time,
) error {
	return store.updateWidgetSnapshots(
		ctx, store.pool, accountID, viewID, snapshots, truncated,
		refreshedAt, updatedAt)
}

func (store *Store) updateWidgetSnapshots(
	ctx context.Context,
	executor refreshSQLExecutor,
	accountID uuid.UUID,
	viewID uuid.UUID,
	snapshots []*deckv1.PullRequestResult,
	truncated bool,
	refreshedAt time.Time,
	updatedAt time.Time,
) error {
	rows, err := executor.Query(ctx, `
		SELECT registration_id, revision, widgets_ciphertext
		FROM deck_device_registrations
		WHERE account_id = $1 AND lease_expires_at > $2
	`, accountID, updatedAt.UTC())
	if err != nil {
		return errors.New("deck database: widget snapshot lookup failed")
	}
	type deviceWidgets struct {
		registrationID uuid.UUID
		revision       int64
		ciphertext     []byte
	}
	var devices []deviceWidgets
	for rows.Next() {
		var device deviceWidgets
		if err := rows.Scan(
			&device.registrationID, &device.revision,
			&device.ciphertext); err != nil {
			rows.Close()
			return errors.New("deck database: widget snapshot lookup failed")
		}
		devices = append(devices, device)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return errors.New("deck database: widget snapshot lookup failed")
	}
	rows.Close()
	for _, device := range devices {
		widgets := &deckv1.Device{}
		if err := store.openProto(
			"device-widgets", device.ciphertext, widgets); err != nil {
			return err
		}
		changed := false
		for _, widget := range widgets.Widgets {
			if widget.GetViewId().GetValue() != viewID.String() {
				continue
			}
			items := widgetSnapshotPullRequestItems(widget.GetPrivacy(), snapshots)
			snapshot := &deckv1.WidgetSnapshot{
				MatchingCount: uint32(len(snapshots)),
				PullRequests:  items,
				Freshness:     deckv1.FreshnessState_FRESHNESS_STATE_FRESH,
				GeneratedAt:   timestamppb.New(refreshedAt.UTC()),
			}
			if proto.Equal(widget.Snapshot, snapshot) {
				continue
			}
			changed = true
			widget.Snapshot = snapshot
			// The count remains the visible retained result count. Truncation
			// is intentionally not converted into a guessed provider total.
			_ = truncated
		}
		if !changed {
			continue
		}
		ciphertext, err := store.sealProto("device-widgets", widgets)
		if err != nil {
			return err
		}
		result, err := executor.Exec(ctx, `
			UPDATE deck_device_registrations
			SET widgets_ciphertext = $1, revision = revision + 1,
			    updated_at = $2
			WHERE registration_id = $3 AND revision = $4
		`, ciphertext, updatedAt.UTC(), device.registrationID, device.revision)
		if err != nil {
			return errors.New("deck database: widget snapshot update failed")
		}
		if result.RowsAffected() == 0 {
			// A concurrent authenticated device write wins. Its next refresh
			// will populate the new widget configuration.
			continue
		}
	}
	return nil
}

func widgetSnapshotPullRequestItems(
	privacy deckv1.WidgetPrivacy,
	snapshots []*deckv1.PullRequestResult,
) []*deckv1.WidgetPullRequestItem {
	if privacy != deckv1.WidgetPrivacy_WIDGET_PRIVACY_REPOSITORY_AND_TITLES {
		return nil
	}
	limit := min(len(snapshots), widgetSnapshotPullRequestLimit)
	items := make([]*deckv1.WidgetPullRequestItem, 0, limit)
	for _, snapshot := range snapshots[:limit] {
		items = append(items, &deckv1.WidgetPullRequestItem{
			Repository: proto.Clone(snapshot.GetRepository()).(*deckv1.RepositoryReference),
			Number:     snapshot.GetNumber(), Title: snapshot.GetTitle(),
		})
	}
	return items
}

func DetailedNotificationText(detail *deckv1.PullRequestDetail) string {
	result := detail.GetResult()
	repository := result.GetRepository()
	if repository == nil {
		return "Deck view updated"
	}
	return strings.TrimSpace(
		repository.GetOwner() + "/" + repository.GetName() + " #" +
			formatPullRequestNumber(result.GetNumber()) + ": " + result.GetTitle())
}

func formatPullRequestNumber(value uint64) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	index := len(buffer)
	for value > 0 {
		index--
		buffer[index] = digits[value%10]
		value /= 10
	}
	return string(buffer[index:])
}
