package postgres

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const sweepAdvisoryLock int64 = 0x6465766875647377

const identityAdvisoryLockNamespace = "devhud-identity-v1"
const purgedIdentityNamespace = "devhud-purged-identity-v1"

type Store struct {
	pool  *pgxpool.Pool
	ids   domain.IDGenerator
	clock domain.Clock
}

func New(pool *pgxpool.Pool, ids domain.IDGenerator, clock domain.Clock) *Store {
	return &Store{pool: pool, ids: ids, clock: clock}
}

func NewPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	configuration, err := parsePoolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	return newPool(ctx, configuration)
}

func NewSweeperPool(ctx context.Context, databaseURL string) (*pgxpool.Pool, error) {
	configuration, err := parsePoolConfig(databaseURL)
	if err != nil {
		return nil, err
	}
	if configuration.MaxConns < 2 {
		return nil, errors.New("sweeper PostgreSQL pool must allow at least 2 connections")
	}
	return newPool(ctx, configuration)
}

func parsePoolConfig(databaseURL string) (*pgxpool.Config, error) {
	configuration, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, errors.New("parse PostgreSQL configuration")
	}
	return configuration, nil
}

func newPool(ctx context.Context, configuration *pgxpool.Config) (*pgxpool.Pool, error) {
	pool, err := pgxpool.NewWithConfig(ctx, configuration)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect to PostgreSQL: %w", err)
	}
	return pool, nil
}

func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

func (s *Store) SchemaCurrent(ctx context.Context) (bool, error) {
	var ledgerExists bool
	if err := s.pool.QueryRow(ctx, "SELECT to_regclass('devhud_schema_migrations') IS NOT NULL").Scan(&ledgerExists); err != nil {
		return false, err
	}
	if !ledgerExists {
		return false, nil
	}
	expected, err := expectedMigrationVersions()
	if err != nil {
		return false, err
	}
	rows, err := s.pool.Query(ctx, "SELECT version FROM devhud_schema_migrations ORDER BY version")
	if err != nil {
		return false, err
	}
	defer rows.Close()
	applied := make([]string, 0, len(expected))
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return false, err
		}
		applied = append(applied, version)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return slices.Equal(applied, expected), nil
}

func (s *Store) ProvisionUser(ctx context.Context, identity domain.Identity) (domain.User, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIdentity(ctx, tx, identity.Issuer, identity.Subject); err != nil {
		return domain.User{}, err
	}

	candidates := make([][]byte, 0, len(identity.FingerprintCandidates)+1)
	candidates = append(candidates, purgedIdentityFingerprint(identity.Issuer, identity.Subject))
	if len(identity.FingerprintCandidates) == 0 {
		candidates = append(candidates, identity.Fingerprint)
	} else {
		candidates = append(candidates, identity.FingerprintCandidates...)
	}
	for _, fingerprint := range candidates {
		var purged bool
		if err := tx.QueryRow(ctx, "SELECT EXISTS (SELECT 1 FROM devhud_purged_identities WHERE identity_fingerprint = $1)", fingerprint).Scan(&purged); err != nil {
			return domain.User{}, err
		}
		if purged {
			return domain.User{}, domain.ErrIdentityPurged
		}
	}

	userID, err := s.ids.New()
	if err != nil {
		return domain.User{}, err
	}
	now := s.clock.Now()
	row := tx.QueryRow(ctx, `
        INSERT INTO devhud_users (
            user_id, logto_issuer, logto_subject, identity_fingerprint,
            display_name, email, deletion_state, administrative_block_state,
            created_at, updated_at
        ) VALUES ($1, $2, $3, $4, $5, $6, 1, 1, $7, $7)
        ON CONFLICT (logto_issuer, logto_subject) DO UPDATE SET
            identity_fingerprint = EXCLUDED.identity_fingerprint,
            display_name = EXCLUDED.display_name,
            email = EXCLUDED.email,
            updated_at = EXCLUDED.updated_at
        RETURNING `+userColumns, userID, identity.Issuer, identity.Subject, identity.Fingerprint, identity.DisplayName, identity.Email, now)
	user, err := scanUser(row)
	if err != nil {
		return domain.User{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) GetSettings(ctx context.Context, userID string) (*domain.Settings, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var deletionState domain.DeletionState
	var blockState domain.AdministrativeBlockState
	if err := tx.QueryRow(ctx, `SELECT deletion_state, administrative_block_state
        FROM devhud_users WHERE user_id = $1 FOR SHARE`, userID).Scan(&deletionState, &blockState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, err
	}
	if blockState == domain.AdministrativeBlockStateBlocked {
		return nil, &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}
	if deletionState != domain.DeletionStateActive {
		return nil, &domain.PermissionError{Failure: domain.PermissionFailureDeletionPending}
	}

	settings, err := getSettingsTx(ctx, tx, userID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return settings, nil
}

func (s *Store) ReplaceSettings(ctx context.Context, userID string, schemaVersion uint32, canonicalJSON []byte, expectedRevision uint64, now time.Time) (domain.Settings, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Settings{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var deletionState domain.DeletionState
	var blockState domain.AdministrativeBlockState
	if err := tx.QueryRow(ctx, `SELECT deletion_state, administrative_block_state
		FROM devhud_users WHERE user_id = $1 FOR UPDATE`, userID).Scan(&deletionState, &blockState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Settings{}, domain.ErrNotFound
		}
		return domain.Settings{}, err
	}
	if blockState == domain.AdministrativeBlockStateBlocked {
		return domain.Settings{}, &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}
	if deletionState != domain.DeletionStateActive {
		return domain.Settings{}, &domain.PermissionError{Failure: domain.PermissionFailureDeletionPending}
	}

	if expectedRevision == 0 {
		row := tx.QueryRow(ctx, `INSERT INTO devhud_settings
            (user_id, schema_version, revision, canonical_json, updated_at)
            VALUES ($1, $2, 1, $3, $4)
            ON CONFLICT (user_id) DO NOTHING
            RETURNING schema_version, revision::text, canonical_json, updated_at`, userID, schemaVersion, canonicalJSON, now)
		settings, scanErr := scanSettings(row)
		if scanErr == nil {
			if err := tx.Commit(ctx); err != nil {
				return domain.Settings{}, err
			}
			return settings, nil
		}
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return domain.Settings{}, scanErr
		}
	} else {
		row := tx.QueryRow(ctx, `UPDATE devhud_settings SET
            schema_version = $2, revision = revision + 1, canonical_json = $3, updated_at = $4
            WHERE user_id = $1 AND revision = $5 AND revision < 18446744073709551615
            RETURNING schema_version, revision::text, canonical_json, updated_at`, userID, schemaVersion, canonicalJSON, now, strconv.FormatUint(expectedRevision, 10))
		settings, scanErr := scanSettings(row)
		if scanErr == nil {
			if err := tx.Commit(ctx); err != nil {
				return domain.Settings{}, err
			}
			return settings, nil
		}
		if !errors.Is(scanErr, pgx.ErrNoRows) {
			return domain.Settings{}, scanErr
		}
	}

	current, err := getSettingsTx(ctx, tx, userID)
	if err != nil {
		return domain.Settings{}, err
	}
	return domain.Settings{}, &domain.RevisionConflict{Expected: expectedRevision, Current: current}
}

func (s *Store) GetAccount(ctx context.Context, userID string) (domain.User, error) {
	user, err := scanUser(s.pool.QueryRow(ctx, "SELECT "+userColumns+" FROM devhud_users WHERE user_id = $1", userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	return user, err
}

func (s *Store) DeleteAccount(ctx context.Context, userID string, now time.Time) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM devhud_users WHERE user_id = $1 FOR UPDATE", userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	if user.DeletionState == domain.DeletionStatePurgeClaimed {
		return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailurePurgeClaimed}
	}
	if user.DeletionState == domain.DeletionStateActive {
		recoverableUntil := now.Add(domain.RecoveryWindow)
		user, err = scanUser(tx.QueryRow(ctx, `UPDATE devhud_users SET
            deletion_state = 2, deletion_requested_at = $2, recoverable_until = $3,
            restore_retry_until = NULL, updated_at = $2
            WHERE user_id = $1 RETURNING `+userColumns, userID, now, recoverableUntil))
		if err != nil {
			return domain.User{}, err
		}
		if err := s.insertAccountAudit(ctx, tx, user, domain.AuditActionAccountDeletionRequested, now); err != nil {
			return domain.User{}, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) RestoreAccount(ctx context.Context, userID string, now time.Time) (domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.User{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	user, err := scanUser(tx.QueryRow(ctx, "SELECT "+userColumns+" FROM devhud_users WHERE user_id = $1 FOR UPDATE", userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.User{}, domain.ErrNotFound
	}
	if err != nil {
		return domain.User{}, err
	}
	switch user.DeletionState {
	case domain.DeletionStateActive:
		if user.RestoreRetryUntil == nil || !now.Before(*user.RestoreRetryUntil) {
			return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailureRecoveryExpired}
		}
	case domain.DeletionStatePending:
		if user.RecoverableUntil == nil || !now.Before(*user.RecoverableUntil) {
			return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailureRecoveryExpired}
		}
		user, err = scanUser(tx.QueryRow(ctx, `UPDATE devhud_users SET
            deletion_state = 1, restore_retry_until = recoverable_until,
            deletion_requested_at = NULL, recoverable_until = NULL, updated_at = $2
            WHERE user_id = $1 RETURNING `+userColumns, userID, now))
		if err != nil {
			return domain.User{}, err
		}
		if err := s.insertAccountAudit(ctx, tx, user, domain.AuditActionAccountRestored, now); err != nil {
			return domain.User{}, err
		}
	case domain.DeletionStatePurgeClaimed:
		return domain.User{}, &domain.AccountStateError{Failure: domain.AccountFailurePurgeClaimed}
	default:
		return domain.User{}, fmt.Errorf("unknown deletion state %d", user.DeletionState)
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.User{}, err
	}
	return user, nil
}

func (s *Store) RecordRequest(ctx context.Context, record domain.RequestLog) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO devhud_request_logs
		(request_log_id, correlation_id, procedure, http_status, rpc_status_code, duration_milliseconds, created_at, expires_at)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''), $6, $7, $8)`, record.ID, record.CorrelationID, record.Procedure,
		record.HTTPStatus, record.RPCStatusCode, record.DurationMilliseconds, record.CreatedAt, record.ExpiresAt)
	return err
}

func (s *Store) RecordAudit(ctx context.Context, event domain.AuditEvent) error {
	_, err := s.pool.Exec(ctx, `INSERT INTO devhud_audit_events
		(audit_event_id, actor_user_id, target_user_id, actor_fingerprint, target_fingerprint, action, created_at, expires_at, target_upload_id, reason)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, NULLIF($10, ''))`, event.ID, event.ActorUserID, event.TargetUserID,
		event.ActorFingerprint, event.TargetFingerprint, event.Action, event.CreatedAt, event.ExpiresAt, event.TargetUploadID, event.Reason)
	return err
}

func (s *Store) ClaimPurgeBatch(ctx context.Context, now time.Time, limit int) ([]domain.User, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT user_id, deletion_state AS previous_deletion_state FROM devhud_users
		WHERE (deletion_state = 2 AND recoverable_until <= $1) OR deletion_state = 3
		ORDER BY (deletion_state = 3), recoverable_until, user_id
		FOR UPDATE SKIP LOCKED
		LIMIT $2
	)
	UPDATE devhud_users u SET deletion_state = 3, updated_at = $1
	FROM candidates c WHERE u.user_id = c.user_id
	RETURNING `+prefixedUserColumns("u")+`, c.previous_deletion_state`, now, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type claimedUser struct {
		user          domain.User
		previousState domain.DeletionState
	}
	claims := make([]claimedUser, 0, limit)
	for rows.Next() {
		var claim claimedUser
		destinations := append(userScanDestinations(&claim.user), &claim.previousState)
		if err := rows.Scan(destinations...); err != nil {
			return nil, err
		}
		claims = append(claims, claim)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	rows.Close()

	users := make([]domain.User, 0, len(claims))
	for _, claim := range claims {
		if claim.previousState == domain.DeletionStatePending {
			if err := s.insertAccountAudit(ctx, tx, claim.user, domain.AuditActionAccountPurgeClaimed, now); err != nil {
				return nil, err
			}
		}
		users = append(users, claim.user)
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return users, nil
}

func (s *Store) CompleteAccountPurge(ctx context.Context, user domain.User, now time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := lockIdentity(ctx, tx, user.Issuer, user.Subject); err != nil {
		return err
	}
	var state domain.DeletionState
	var currentFingerprint []byte
	if err := tx.QueryRow(ctx, `SELECT deletion_state, identity_fingerprint
        FROM devhud_users WHERE user_id = $1 FOR UPDATE`, user.ID).Scan(&state, &currentFingerprint); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if state != domain.DeletionStatePurgeClaimed {
		return fmt.Errorf("account %s is not purge-claimed", user.ID)
	}
	if _, err := tx.Exec(ctx, `INSERT INTO devhud_purged_identities (identity_fingerprint, purged_at)
		VALUES ($1, $2) ON CONFLICT (identity_fingerprint) DO NOTHING`, purgedIdentityFingerprint(user.Issuer, user.Subject), now); err != nil {
		return err
	}
	user.IdentityFingerprint = currentFingerprint
	if err := s.insertAccountAudit(ctx, tx, user, domain.AuditActionAccountPurged, now); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE devhud_audit_events SET actor_user_id = NULL
        WHERE actor_user_id = $1`, user.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE devhud_audit_events SET target_user_id = NULL
        WHERE target_user_id = $1`, user.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, "DELETE FROM devhud_users WHERE user_id = $1", user.ID); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func (s *Store) SubmitCrashReport(ctx context.Context, userID string, report domain.CrashReport) (domain.CrashReport, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.CrashReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var deletionState domain.DeletionState
	var blockState domain.AdministrativeBlockState
	if err := tx.QueryRow(ctx, `SELECT deletion_state, administrative_block_state
        FROM devhud_users WHERE user_id = $1 FOR UPDATE`, userID).Scan(&deletionState, &blockState); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.CrashReport{}, domain.ErrNotFound
		}
		return domain.CrashReport{}, err
	}
	if blockState == domain.AdministrativeBlockStateBlocked {
		return domain.CrashReport{}, &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}
	if deletionState != domain.DeletionStateActive {
		return domain.CrashReport{}, &domain.PermissionError{Failure: domain.PermissionFailureDeletionPending}
	}

	var existingDigest []byte
	existingErr := tx.QueryRow(ctx, `SELECT crash_report_id::text, payload_sha256, accepted_at, expires_at
        FROM devhud_crash_reports
        WHERE owner_user_id = $1 AND client_correlation_id = $2`, userID, report.ClientCorrelationID).
		Scan(&report.ID, &existingDigest, &report.AcceptedAt, &report.ExpiresAt)
	if existingErr == nil {
		if !bytes.Equal(existingDigest, report.PayloadSHA256) {
			return domain.CrashReport{}, domain.ErrCorrelationConflict
		}
		report.OwnerUserID = userID
		if err := tx.Commit(ctx); err != nil {
			return domain.CrashReport{}, err
		}
		return report, nil
	}
	if !errors.Is(existingErr, pgx.ErrNoRows) {
		return domain.CrashReport{}, existingErr
	}

	var retainedReports int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM devhud_crash_reports
        WHERE owner_user_id = $1 AND expires_at > $2`, userID, report.AcceptedAt).Scan(&retainedReports); err != nil {
		return domain.CrashReport{}, err
	}
	if retainedReports >= domain.CrashReportMaximumRetainedPerUser {
		return domain.CrashReport{}, domain.ErrCrashReportQuota
	}

	reportID, err := s.ids.New()
	if err != nil {
		return domain.CrashReport{}, err
	}
	command, err := tx.Exec(ctx, `INSERT INTO devhud_crash_reports (
        crash_report_id, owner_user_id, request_correlation_id, client_correlation_id,
        payload_sha256, report_schema_version, app_version, build_id, platform,
        architecture, os_version, tauri_revision, cef_revision, occurred_at,
        component, severity, error_code, redacted_summary, redacted_stack_trace,
        related_correlation_ids, duration_milliseconds, accepted_at, expires_at
    ) VALUES (
        $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14,
        $15, $16, $17, $18, $19, $20::uuid[], $21, $22, $23
    ) ON CONFLICT (owner_user_id, client_correlation_id) DO NOTHING`,
		reportID, userID, report.RequestCorrelationID, report.ClientCorrelationID,
		report.PayloadSHA256, report.ReportSchemaVersion, report.AppVersion, report.BuildID,
		report.Platform, report.Architecture, report.OSVersion, report.TauriRevision,
		report.CEFRevision, report.OccurredAt, report.Component, report.Severity,
		report.ErrorCode, report.RedactedSummary, report.RedactedStackTrace,
		report.RelatedCorrelationIDs, report.DurationMilliseconds, report.AcceptedAt, report.ExpiresAt)
	if err != nil {
		return domain.CrashReport{}, err
	}
	if command.RowsAffected() == 0 {
		if err := tx.QueryRow(ctx, `SELECT crash_report_id::text, payload_sha256, accepted_at, expires_at
            FROM devhud_crash_reports
            WHERE owner_user_id = $1 AND client_correlation_id = $2`, userID, report.ClientCorrelationID).
			Scan(&report.ID, &existingDigest, &report.AcceptedAt, &report.ExpiresAt); err != nil {
			return domain.CrashReport{}, err
		}
		if !bytes.Equal(existingDigest, report.PayloadSHA256) {
			return domain.CrashReport{}, domain.ErrCorrelationConflict
		}
	} else {
		report.ID = reportID
	}
	report.OwnerUserID = userID
	if err := tx.Commit(ctx); err != nil {
		return domain.CrashReport{}, err
	}
	return report, nil
}

func (s *Store) PruneRetention(ctx context.Context, now time.Time, limit int) (domain.RetentionResult, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.RetentionResult{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	requestResult, err := tx.Exec(ctx, `WITH expired AS (
		SELECT request_log_id FROM devhud_request_logs
		WHERE expires_at <= $1
		ORDER BY expires_at, request_log_id
		LIMIT $2
	)
	DELETE FROM devhud_request_logs logs
	USING expired WHERE logs.request_log_id = expired.request_log_id`, now, limit)
	if err != nil {
		return domain.RetentionResult{}, err
	}
	auditResult, err := tx.Exec(ctx, `WITH expired AS (
		SELECT audit_event_id FROM devhud_audit_events
		WHERE expires_at <= $1
		ORDER BY expires_at, audit_event_id
		LIMIT $2
	)
	DELETE FROM devhud_audit_events events
	USING expired WHERE events.audit_event_id = expired.audit_event_id`, now, limit)
	if err != nil {
		return domain.RetentionResult{}, err
	}
	crashResult, err := tx.Exec(ctx, `WITH expired AS (
		SELECT crash_report_id FROM devhud_crash_reports
		WHERE expires_at <= $1
		ORDER BY expires_at, crash_report_id
		LIMIT $2
	)
	DELETE FROM devhud_crash_reports reports
	USING expired WHERE reports.crash_report_id = expired.crash_report_id`, now, limit)
	if err != nil {
		return domain.RetentionResult{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.RetentionResult{}, err
	}
	return domain.RetentionResult{
		RequestLogsDeleted:  requestResult.RowsAffected(),
		AuditEventsDeleted:  auditResult.RowsAffected(),
		CrashReportsDeleted: crashResult.RowsAffected(),
	}, nil
}

func (s *Store) TryLock(ctx context.Context) (func(context.Context) error, bool, error) {
	connection, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, err
	}
	var acquired bool
	if err := connection.QueryRow(ctx, "SELECT pg_try_advisory_lock($1)", sweepAdvisoryLock).Scan(&acquired); err != nil {
		return nil, false, errors.Join(err, discardPoolConnection(connection))
	}
	if !acquired {
		connection.Release()
		return func(context.Context) error { return nil }, false, nil
	}
	return func(unlockContext context.Context) error {
		var unlocked bool
		if err := connection.QueryRow(unlockContext, "SELECT pg_advisory_unlock($1)", sweepAdvisoryLock).Scan(&unlocked); err != nil {
			return errors.Join(err, discardPoolConnection(connection))
		}
		if !unlocked {
			return errors.Join(errors.New("sweeper advisory lock was not held"), discardPoolConnection(connection))
		}
		connection.Release()
		return nil
	}, true, nil
}

func discardPoolConnection(connection *pgxpool.Conn) error {
	return connection.Hijack().Close(context.Background())
}

const userColumns = `user_id::text, logto_issuer, logto_subject, identity_fingerprint,
    display_name, email, deletion_state, administrative_block_state,
    created_at, updated_at, deletion_requested_at, recoverable_until, restore_retry_until`

func prefixedUserColumns(prefix string) string {
	return prefix + `.user_id::text, ` + prefix + `.logto_issuer, ` + prefix + `.logto_subject, ` + prefix + `.identity_fingerprint,
    ` + prefix + `.display_name, ` + prefix + `.email, ` + prefix + `.deletion_state, ` + prefix + `.administrative_block_state,
    ` + prefix + `.created_at, ` + prefix + `.updated_at, ` + prefix + `.deletion_requested_at, ` + prefix + `.recoverable_until,
    ` + prefix + `.restore_retry_until`
}

type rowScanner interface {
	Scan(...any) error
}

func scanUser(row rowScanner) (domain.User, error) {
	var user domain.User
	err := row.Scan(userScanDestinations(&user)...)
	return user, err
}

func userScanDestinations(user *domain.User) []any {
	return []any{&user.ID, &user.Issuer, &user.Subject, &user.IdentityFingerprint, &user.DisplayName, &user.Email,
		&user.DeletionState, &user.AdministrativeBlockState, &user.CreatedAt, &user.UpdatedAt,
		&user.DeletionRequestedAt, &user.RecoverableUntil, &user.RestoreRetryUntil}
}

func lockIdentity(ctx context.Context, tx pgx.Tx, issuer, subject string) error {
	_, err := tx.Exec(ctx, "SELECT pg_advisory_xact_lock($1)", identityAdvisoryLockKey(issuer, subject))
	return err
}

func identityAdvisoryLockKey(issuer, subject string) int64 {
	return int64(binary.BigEndian.Uint64(identityDigest(identityAdvisoryLockNamespace, issuer, subject)[:8]))
}

func purgedIdentityFingerprint(issuer, subject string) []byte {
	return identityDigest(purgedIdentityNamespace, issuer, subject)
}

func identityDigest(namespace, issuer, subject string) []byte {
	hash := sha256.New()
	_, _ = hash.Write([]byte(namespace))
	var encodedLength [8]byte
	binary.BigEndian.PutUint64(encodedLength[:], uint64(len(issuer)))
	_, _ = hash.Write(encodedLength[:])
	_, _ = hash.Write([]byte(issuer))
	binary.BigEndian.PutUint64(encodedLength[:], uint64(len(subject)))
	_, _ = hash.Write(encodedLength[:])
	_, _ = hash.Write([]byte(subject))
	return hash.Sum(nil)
}

func scanSettings(row rowScanner) (domain.Settings, error) {
	var settings domain.Settings
	var revision string
	if err := row.Scan(&settings.SchemaVersion, &revision, &settings.CanonicalJSON, &settings.UpdatedAt); err != nil {
		return domain.Settings{}, err
	}
	parsed, err := strconv.ParseUint(revision, 10, 64)
	if err != nil {
		return domain.Settings{}, fmt.Errorf("parse settings revision: %w", err)
	}
	settings.Revision = parsed
	return settings, nil
}

func getSettingsTx(ctx context.Context, tx pgx.Tx, userID string) (*domain.Settings, error) {
	settings, err := scanSettings(tx.QueryRow(ctx, `SELECT schema_version, revision::text, canonical_json, updated_at
        FROM devhud_settings WHERE user_id = $1`, userID))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &settings, nil
}

func (s *Store) insertAccountAudit(ctx context.Context, tx pgx.Tx, user domain.User, action domain.AuditAction, now time.Time) error {
	id, err := s.ids.New()
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO devhud_audit_events
        (audit_event_id, actor_user_id, target_user_id, actor_fingerprint, target_fingerprint, action, created_at, expires_at)
        VALUES ($1, $2, $2, $3, $3, $4, $5, $6)`, id, user.ID, user.IdentityFingerprint, action, now, now.Add(domain.AuditRetention))
	return err
}
