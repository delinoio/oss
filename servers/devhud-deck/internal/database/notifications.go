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
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const notificationRetention = 30 * 24 * time.Hour

type NotificationEventWrite struct {
	Transition deckv1.NotificationTransition
	Snapshot   *deckv1.PullRequestResult
}

type NotificationEventRecord struct {
	ViewID     uuid.UUID
	ViewerHash [32]byte
	Transition deckv1.NotificationTransition
	Reference  *deckv1.PullRequestReference
	Detail     *deckv1.PullRequestDetail
	CreatedAt  time.Time
	ExpiresAt  time.Time
}

func (store *Store) CreateNotificationEvents(
	ctx context.Context,
	viewID uuid.UUID,
	viewerHash [32]byte,
	events []NotificationEventWrite,
	now time.Time,
) error {
	if err := store.PruneNotificationHistory(ctx, now); err != nil {
		return err
	}
	for _, event := range events {
		repository := event.Snapshot.GetRepository()
		if event.Transition <
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
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO deck_notification_events (
				event_id, view_id, opaque_event_id, transition,
				created_at, expires_at, viewer_hash, repository_hash,
				pull_request_number, detail_ciphertext
			) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		`, eventID, viewID, verifier[:], int16(event.Transition), now.UTC(),
			now.UTC().Add(notificationRetention), viewerHash[:],
			repositoryHash[:], int64(event.Snapshot.GetNumber()),
			ciphertext); err != nil {
			return errors.New("deck database: notification insert failed")
		}
	}
	return nil
}

func (store *Store) PruneNotificationHistory(
	ctx context.Context,
	now time.Time,
) error {
	if _, err := store.pool.Exec(ctx,
		"DELETE FROM deck_notification_events WHERE expires_at <= $1",
		now.UTC()); err != nil {
		return errors.New("deck database: notification retention failed")
	}
	return nil
}

func (store *Store) GetNotificationEvent(
	ctx context.Context,
	opaque string,
	now time.Time,
) (NotificationEventRecord, error) {
	if opaque == "" {
		return NotificationEventRecord{}, ErrNotFound
	}
	verifier := security.GrantVerifier(opaque)
	var record NotificationEventRecord
	var viewerHash, ciphertext []byte
	var transition int16
	var pullRequestNumber int64
	err := store.pool.QueryRow(ctx, `
		SELECT view_id, viewer_hash, transition, pull_request_number,
		       detail_ciphertext, created_at, expires_at
		FROM deck_notification_events
		WHERE opaque_event_id = $1 AND expires_at > $2
		  AND viewer_hash IS NOT NULL
		  AND detail_ciphertext IS NOT NULL
	`, verifier[:], now.UTC()).Scan(
		&record.ViewID, &viewerHash, &transition, &pullRequestNumber,
		&ciphertext, &record.CreatedAt, &record.ExpiresAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return NotificationEventRecord{}, ErrNotFound
	}
	if err != nil || len(viewerHash) != 32 {
		return NotificationEventRecord{}, errors.New(
			"deck database: notification lookup failed")
	}
	copy(record.ViewerHash[:], viewerHash)
	record.Transition = deckv1.NotificationTransition(transition)
	record.Detail = &deckv1.PullRequestDetail{}
	if err := store.openProto(
		"notification-detail", ciphertext, record.Detail); err != nil {
		return NotificationEventRecord{}, err
	}
	repository := record.Detail.GetResult().GetRepository()
	if repository == nil || pullRequestNumber <= 0 ||
		record.Detail.GetResult().GetNumber() != uint64(pullRequestNumber) {
		return NotificationEventRecord{}, errors.New(
			"deck database: invalid notification record")
	}
	record.Reference = &deckv1.PullRequestReference{
		Repository: proto.Clone(repository).(*deckv1.RepositoryReference),
		Number:     uint64(pullRequestNumber),
	}
	return record, nil
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

func (store *Store) UpdateWidgetSnapshots(
	ctx context.Context,
	accountID uuid.UUID,
	viewID uuid.UUID,
	snapshots []*deckv1.PullRequestResult,
	truncated bool,
	refreshedAt time.Time,
) error {
	rows, err := store.pool.Query(ctx, `
		SELECT registration_id, revision, widgets_ciphertext
		FROM deck_device_registrations
		WHERE account_id = $1 AND lease_expires_at > $2
	`, accountID, refreshedAt.UTC())
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
			changed = true
			items := make([]*deckv1.WidgetPullRequestItem, 0, len(snapshots))
			if widget.GetPrivacy() ==
				deckv1.WidgetPrivacy_WIDGET_PRIVACY_REPOSITORY_AND_TITLES {
				for _, snapshot := range snapshots {
					items = append(items, &deckv1.WidgetPullRequestItem{
						Repository: proto.Clone(snapshot.GetRepository()).(*deckv1.RepositoryReference),
						Number:     snapshot.GetNumber(), Title: snapshot.GetTitle(),
					})
				}
			}
			widget.Snapshot = &deckv1.WidgetSnapshot{
				MatchingCount: uint32(len(snapshots)),
				PullRequests:  items,
				Freshness:     deckv1.FreshnessState_FRESHNESS_STATE_FRESH,
				GeneratedAt:   timestamppb.New(refreshedAt.UTC()),
			}
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
		result, err := store.pool.Exec(ctx, `
			UPDATE deck_device_registrations
			SET widgets_ciphertext = $1, revision = revision + 1,
			    updated_at = $2
			WHERE registration_id = $3 AND revision = $4
		`, ciphertext, refreshedAt.UTC(), device.registrationID, device.revision)
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
