package postgres

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const adminAuditColumns = `audit_event_id, actor_user_id, target_user_id, target_upload_id,
	action, COALESCE(reason, ''), created_at, correlation_id, outcome, COALESCE(rejection_reason, 0)`

func (s *Store) ListUsers(ctx context.Context, query string, cursor *domain.UserCursor, limit uint32) (domain.UserList, error) {
	pattern := escapeLike(query) + "%"
	var cursorTime *time.Time
	var cursorID *string
	if cursor != nil {
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.UserID
	}
	rows, err := s.pool.Query(ctx, `SELECT `+userColumns+` FROM devhud_users
		WHERE ($1 = '%' OR search_display_name LIKE $1 ESCAPE '\' OR search_email LIKE $1 ESCAPE '\' OR search_logto_subject LIKE $1 ESCAPE '\')
		AND ($2::timestamptz IS NULL OR (created_at, user_id) < ($2, $3::uuid))
		ORDER BY created_at DESC, user_id DESC LIMIT $4`, pattern, cursorTime, cursorID, int(limit)+1)
	if err != nil {
		return domain.UserList{}, err
	}
	defer rows.Close()
	result := domain.UserList{Users: make([]domain.User, 0, limit)}
	for rows.Next() {
		user, err := scanUser(rows)
		if err != nil {
			return domain.UserList{}, err
		}
		result.Users = append(result.Users, user)
	}
	if err := rows.Err(); err != nil {
		return domain.UserList{}, err
	}
	if len(result.Users) > int(limit) {
		last := result.Users[limit-1]
		result.Next = &domain.UserCursor{CreatedAt: last.CreatedAt, UserID: last.ID}
		result.Users = result.Users[:limit]
	}
	return result, nil
}

func escapeLike(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `%`, `\%`)
	return strings.ReplaceAll(value, `_`, `\_`)
}

func (s *Store) SetUserBlocked(ctx context.Context, actorID, targetID string, expected, target domain.AdministrativeBlockState, event domain.AuditEvent, now time.Time) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureAdminActor(ctx, tx, actorID); err != nil {
		var permission *domain.PermissionError
		if errors.As(err, &permission) {
			event.Outcome = domain.AuditOutcomeRejected
			event.RejectionReason = domain.AuditRejectionActorBlocked
			if auditErr := insertAdministratorAudit(ctx, tx, event); auditErr != nil {
				return domain.User{}, auditErr
			}
			if commitErr := tx.Commit(ctx); commitErr != nil {
				return domain.User{}, commitErr
			}
		}
		return domain.User{}, err
	}
	user, err := scanUser(tx.QueryRow(ctx, `SELECT `+userColumns+` FROM devhud_users WHERE user_id = $1 FOR UPDATE`, targetID))
	if errors.Is(err, pgx.ErrNoRows) {
		event.Outcome = domain.AuditOutcomeRejected
		event.RejectionReason = domain.AuditRejectionTargetNotFound
		if err := insertAdministratorAudit(ctx, tx, event); err != nil {
			return domain.User{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, err
		}
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	if user.AdministrativeBlockState != expected {
		event.Outcome = domain.AuditOutcomeRejected
		event.RejectionReason = domain.AuditRejectionConcurrentUpdate
		if err := insertAdministratorAudit(ctx, tx, event); err != nil {
			return domain.User{}, err
		}
		if err := tx.Commit(ctx); err != nil {
			return domain.User{}, err
		}
		return domain.User{}, &domain.AdminConflictError{User: &user}
	}
	user, err = scanUser(tx.QueryRow(ctx, `UPDATE devhud_users SET administrative_block_state = $2, updated_at = $3
		WHERE user_id = $1 RETURNING `+userColumns, targetID, target, now))
	if err != nil {
		return domain.User{}, err
	}
	event.Outcome = domain.AuditOutcomeAccepted
	if err := insertAdministratorAudit(ctx, tx, event); err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func ensureAdminActor(ctx context.Context, tx pgx.Tx, actorID string) error {
	var deletion domain.DeletionState
	var blocked domain.AdministrativeBlockState
	if err := tx.QueryRow(ctx, `SELECT deletion_state, administrative_block_state FROM devhud_users WHERE user_id = $1 FOR SHARE`, actorID).Scan(&deletion, &blocked); err != nil {
		return err
	}
	if deletion != domain.DeletionStateActive || blocked == domain.AdministrativeBlockStateBlocked {
		return &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}
	return nil
}

func (s *Store) RecordAdministratorAudit(ctx context.Context, event domain.AuditEvent) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO devhud_audit_events
		(audit_event_id, actor_user_id, target_user_id, actor_fingerprint, target_fingerprint, target_upload_id,
		action, reason, created_at, expires_at, correlation_id, outcome, rejection_reason)
		VALUES ($1, (SELECT user_id FROM devhud_users WHERE user_id = $2),
		(SELECT user_id FROM devhud_users WHERE user_id = $3),
		(SELECT identity_fingerprint FROM devhud_users WHERE user_id = $2),
		(SELECT identity_fingerprint FROM devhud_users WHERE user_id = $3),
		(SELECT upload_id FROM devhud_uploads WHERE upload_id = $4),
		$5, NULLIF($6, ''), $7, $8, $9, $10, NULLIF($11, 0))`, event.ID, validUUIDPointer(event.ActorUserID), validUUIDPointer(event.TargetUserID),
		validUUIDPointer(event.TargetUploadID), event.Action, event.Reason, event.CreatedAt, event.ExpiresAt, event.CorrelationID, event.Outcome, event.RejectionReason)
	return err
}

func insertAdministratorAudit(ctx context.Context, tx pgx.Tx, event domain.AuditEvent) error {
	command, err := tx.Exec(ctx, `INSERT INTO devhud_audit_events
		(audit_event_id, actor_user_id, target_user_id, actor_fingerprint, target_fingerprint, target_upload_id,
		action, reason, created_at, expires_at, correlation_id, outcome, rejection_reason)
		VALUES ($1, (SELECT user_id FROM devhud_users WHERE user_id = $2),
		(SELECT user_id FROM devhud_users WHERE user_id = $3),
		(SELECT identity_fingerprint FROM devhud_users WHERE user_id = $2),
		(SELECT identity_fingerprint FROM devhud_users WHERE user_id = $3),
		(SELECT upload_id FROM devhud_uploads WHERE upload_id = $4),
		$5, NULLIF($6, ''), $7, $8, $9, $10, NULLIF($11, 0))`, event.ID, validUUIDPointer(event.ActorUserID), validUUIDPointer(event.TargetUserID),
		validUUIDPointer(event.TargetUploadID), event.Action, event.Reason, event.CreatedAt, event.ExpiresAt, event.CorrelationID, event.Outcome, event.RejectionReason)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func validUUIDPointer(value *string) any {
	if value == nil {
		return nil
	}
	parsed, err := uuid.Parse(*value)
	if err != nil || parsed.Version() != 7 || parsed.String() != *value {
		return nil
	}
	return *value
}

func (s *Store) ListAuditEvents(ctx context.Context, filters domain.AuditFilters, cursor *domain.AuditCursor, limit uint32) (domain.AuditList, error) {
	actions := make([]int16, len(filters.Actions))
	for index, value := range filters.Actions {
		actions[index] = int16(value)
	}
	outcomes := make([]int16, len(filters.Outcomes))
	for index, value := range filters.Outcomes {
		outcomes[index] = int16(value)
	}
	var cursorTime *time.Time
	var cursorID *string
	if cursor != nil {
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.AuditID
	}
	rows, err := s.pool.Query(ctx, `SELECT `+adminAuditColumns+` FROM devhud_audit_events
		WHERE ($1::uuid IS NULL OR actor_user_id = $1) AND ($2::uuid IS NULL OR target_user_id = $2)
		AND ($3::uuid IS NULL OR target_upload_id = $3) AND ($4::uuid IS NULL OR correlation_id = $4)
		AND (cardinality($5::smallint[]) = 0 OR action = ANY($5))
		AND (cardinality($6::smallint[]) = 0 OR outcome = ANY($6))
		AND ($7::timestamptz IS NULL OR (created_at, audit_event_id) < ($7, $8::uuid))
		ORDER BY created_at DESC, audit_event_id DESC LIMIT $9`, nullableUUID(filters.ActorUserID), nullableUUID(filters.TargetUserID),
		nullableUUID(filters.TargetUploadID), nullableUUID(filters.CorrelationID), actions, outcomes, cursorTime, cursorID, int(limit)+1)
	if err != nil {
		return domain.AuditList{}, err
	}
	defer rows.Close()
	result := domain.AuditList{Events: make([]domain.AuditEvent, 0, limit)}
	for rows.Next() {
		var event domain.AuditEvent
		if err := rows.Scan(&event.ID, &event.ActorUserID, &event.TargetUserID, &event.TargetUploadID, &event.Action,
			&event.Reason, &event.CreatedAt, &event.CorrelationID, &event.Outcome, &event.RejectionReason); err != nil {
			return domain.AuditList{}, err
		}
		result.Events = append(result.Events, event)
	}
	if err := rows.Err(); err != nil {
		return domain.AuditList{}, err
	}
	if len(result.Events) > int(limit) {
		last := result.Events[limit-1]
		result.Next = &domain.AuditCursor{CreatedAt: last.CreatedAt, AuditID: last.ID}
		result.Events = result.Events[:limit]
	}
	return result, nil
}

var _ domain.AdminRepository = (*Store)(nil)
