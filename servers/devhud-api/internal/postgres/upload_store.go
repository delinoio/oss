package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/delinoio/oss/servers/devhud-api/internal/domain"
	"github.com/delinoio/oss/servers/devhud-api/internal/idgen"
	"github.com/jackc/pgx/v5"
)

const uploadSelect = `
SELECT u.upload_id::text, u.owner_user_id::text, u.submission_id::text,
       u.upload_group_id::text, u.reservation_id::text, u.public_id,
       u.staging_id, u.staging_generation, u.expected_size_bytes,
       u.expected_sha256, u.state, COALESCE(u.staging_etag, ''),
       COALESCE(u.public_etag, ''), COALESCE(u.replacement_etag, ''),
       COALESCE(u.width, 0), COALESCE(u.height, 0), u.created_at,
       r.signed_url_expires_at, r.staging_expires_at, u.finalized_at,
       u.removed_at, COALESCE(u.removal_reason, 0),
       COALESCE(u.operation_token, '')
FROM devhud_uploads u
JOIN devhud_upload_reservations r ON r.reservation_id = u.reservation_id`

func (s *Store) CreateUpload(ctx context.Context, command domain.CreateUpload, sign func(context.Context, domain.UploadReservation) (domain.SignedPUT, error)) (domain.UploadReservation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return domain.UploadReservation{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureUploadEligible(ctx, tx, command.OwnerUserID, true); err != nil {
		return domain.UploadReservation{}, err
	}

	var signedCount int64
	var retryAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT count(*), min(created_at + interval '1 hour')
		FROM devhud_upload_reservations
		WHERE owner_user_id = $1 AND created_at > $2::timestamptz - interval '1 hour'`, command.OwnerUserID, command.Now).Scan(&signedCount, &retryAt); err != nil {
		return domain.UploadReservation{}, err
	}
	if uint64(signedCount) >= domain.UploadMaximumURLsPerHour {
		return domain.UploadReservation{}, &domain.QuotaError{Quota: domain.QuotaSignedURLs, Limit: domain.UploadMaximumURLsPerHour, Observed: uint64(signedCount) + 1, RetryAt: valueOrZero(retryAt)}
	}

	submissionID, groupID, err := s.resolveUploadTarget(ctx, tx, command)
	if err != nil {
		return domain.UploadReservation{}, err
	}
	uploadID, err := s.ids.New()
	if err != nil {
		return domain.UploadReservation{}, err
	}
	reservationID, err := s.ids.New()
	if err != nil {
		return domain.UploadReservation{}, err
	}
	publicID, err := idgen.Opaque()
	if err != nil {
		return domain.UploadReservation{}, err
	}
	stagingID, err := idgen.Opaque()
	if err != nil {
		return domain.UploadReservation{}, err
	}
	signedExpiry := command.Now.Add(domain.UploadSignedURLLifetime)
	stagingExpiry := command.Now.Add(domain.UploadStagingLifetime)
	if _, err := tx.Exec(ctx, `INSERT INTO devhud_upload_reservations
		(reservation_id, owner_user_id, created_at, signed_url_expires_at, staging_expires_at)
		VALUES ($1, $2, $3, $4, $5)`, reservationID, command.OwnerUserID, command.Now, signedExpiry, stagingExpiry); err != nil {
		return domain.UploadReservation{}, err
	}
	var generation int64
	if err := tx.QueryRow(ctx, `INSERT INTO devhud_uploads
		(upload_id, owner_user_id, submission_id, upload_group_id, reservation_id,
		 public_id, staging_id, expected_size_bytes, expected_sha256, content_type, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, 1, $10)
		RETURNING staging_generation`, uploadID, command.OwnerUserID, submissionID, groupID,
		reservationID, publicID, stagingID, int64(command.SizeBytes), command.SHA256[:], command.Now).Scan(&generation); err != nil {
		return domain.UploadReservation{}, err
	}
	reservation := domain.UploadReservation{
		UploadID: uploadID, OwnerUserID: command.OwnerUserID, SubmissionID: submissionID,
		UploadGroupID: groupID, ReservationID: reservationID, PublicID: publicID,
		StagingID: stagingID, StagingGeneration: uint64(generation), SizeBytes: command.SizeBytes,
		SHA256: command.SHA256, CreatedAt: command.Now, SignedURLExpiresAt: signedExpiry,
		StagingExpiresAt: stagingExpiry,
	}
	material, err := sign(ctx, reservation)
	if err != nil {
		// The transaction owns the quota reservation. Returning before commit
		// removes the reservation, submission, and group together.
		return domain.UploadReservation{}, fmt.Errorf("issue signed PUT: %w", err)
	}
	reservation.SignedPUT = material
	if err := tx.Commit(ctx); err != nil {
		return domain.UploadReservation{}, err
	}
	return reservation, nil
}

func (s *Store) resolveUploadTarget(ctx context.Context, tx pgx.Tx, command domain.CreateUpload) (string, string, error) {
	switch command.Target.Kind {
	case domain.UploadTargetNewSubmission:
		submissionID, err := s.ids.New()
		if err != nil {
			return "", "", err
		}
		groupID, err := s.ids.New()
		if err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devhud_submissions (submission_id, owner_user_id, created_at) VALUES ($1, $2, $3)`, submissionID, command.OwnerUserID, command.Now); err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devhud_upload_groups (upload_group_id, submission_id, owner_user_id, created_at) VALUES ($1, $2, $3, $4)`, groupID, submissionID, command.OwnerUserID, command.Now); err != nil {
			return "", "", err
		}
		return submissionID, groupID, nil
	case domain.UploadTargetNewGroup:
		if !ownedSubmission(ctx, tx, command.Target.SubmissionID, command.OwnerUserID) {
			return "", "", domain.ErrNotFound
		}
		groupID, err := s.ids.New()
		if err != nil {
			return "", "", err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO devhud_upload_groups (upload_group_id, submission_id, owner_user_id, created_at) VALUES ($1, $2, $3, $4)`, groupID, command.Target.SubmissionID, command.OwnerUserID, command.Now); err != nil {
			return "", "", err
		}
		return command.Target.SubmissionID, groupID, nil
	case domain.UploadTargetExistingGroup:
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devhud_upload_groups WHERE upload_group_id = $1 AND submission_id = $2 AND owner_user_id = $3)`, command.Target.UploadGroupID, command.Target.SubmissionID, command.OwnerUserID).Scan(&exists); err != nil {
			return "", "", err
		}
		if !exists {
			return "", "", domain.ErrNotFound
		}
		return command.Target.SubmissionID, command.Target.UploadGroupID, nil
	default:
		return "", "", &domain.UploadError{Failure: domain.UploadFailureBindingMismatch}
	}
}

func ownedSubmission(ctx context.Context, tx pgx.Tx, submissionID, ownerID string) bool {
	var exists bool
	return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM devhud_submissions WHERE submission_id = $1 AND owner_user_id = $2)`, submissionID, ownerID).Scan(&exists) == nil && exists
}

func (s *Store) GetUploadForFinalize(ctx context.Context, ownerID string, binding domain.UploadBinding, now time.Time) (domain.Upload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Upload{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureUploadEligible(ctx, tx, ownerID, false); err != nil {
		return domain.Upload{}, err
	}
	upload, err := scanUpload(tx.QueryRow(ctx, uploadSelect+` WHERE u.upload_id = $1 AND u.owner_user_id = $2`, binding.UploadID, ownerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureReservationMissing}
		}
		return domain.Upload{}, err
	}
	if err := validateBinding(upload, binding, now); err != nil {
		var uploadError *domain.UploadError
		if errors.As(err, &uploadError) && uploadError.Failure == domain.UploadFailureReservationExpired {
			return upload, err
		}
		return domain.Upload{}, err
	}
	if upload.State == domain.UploadStateFinalized {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureAlreadyFinalized}
	}
	if upload.State != domain.UploadStatePending && upload.State != domain.UploadStatePublishing {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureInvalidState}
	}
	return upload, nil
}

func (s *Store) ClaimUploadPromotion(ctx context.Context, ownerID string, binding domain.UploadBinding, object domain.UploadObject, width, height uint32, token string, now time.Time) (domain.Upload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Upload{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := ensureUploadEligible(ctx, tx, ownerID, true); err != nil {
		return domain.Upload{}, err
	}
	upload, err := scanUpload(tx.QueryRow(ctx, uploadSelect+` WHERE u.upload_id = $1 AND u.owner_user_id = $2 FOR UPDATE OF u`, binding.UploadID, ownerID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureReservationMissing}
		}
		return domain.Upload{}, err
	}
	if err := validateBinding(upload, binding, now); err != nil {
		return domain.Upload{}, err
	}
	if upload.State == domain.UploadStateFinalized {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureAlreadyFinalized}
	}
	var operationExpiry *time.Time
	if err := tx.QueryRow(ctx, `SELECT operation_expires_at FROM devhud_uploads WHERE upload_id = $1`, upload.UploadID).Scan(&operationExpiry); err != nil {
		return domain.Upload{}, err
	}
	if upload.State != domain.UploadStatePending && !(upload.State == domain.UploadStatePublishing && (operationExpiry == nil || !now.Before(*operationExpiry))) {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureInvalidState}
	}
	if object.ETag != binding.ObservedETag {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureStagingObjectChanged}
	}
	if _, err := tx.Exec(ctx, `SELECT 1 FROM devhud_submissions WHERE submission_id = $1 AND owner_user_id = $2 FOR UPDATE`, upload.SubmissionID, ownerID); err != nil {
		return domain.Upload{}, err
	}
	if err := enforceFinalizeQuotas(ctx, tx, upload, now); err != nil {
		return domain.Upload{}, err
	}
	row := tx.QueryRow(ctx, `UPDATE devhud_uploads SET state = 2, staging_etag = $3,
		width = $4, height = $5, quota_charged_at = COALESCE(quota_charged_at, $6),
		operation_token = $7, operation_expires_at = $8
		WHERE upload_id = $1 AND owner_user_id = $2 RETURNING `+uploadSelectColumns(),
		upload.UploadID, ownerID, object.ETag, int32(width), int32(height), now, token, now.Add(domain.UploadOperationLease))
	upload, err = scanUpload(row)
	if err != nil {
		return domain.Upload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Upload{}, err
	}
	return upload, nil
}

func enforceFinalizeQuotas(ctx context.Context, tx pgx.Tx, upload domain.Upload, now time.Time) error {
	var submissionCount int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM devhud_uploads WHERE submission_id = $1 AND upload_id <> $2
		AND (state IN (2, 3) OR (state = 4 AND finalized_at IS NOT NULL))`, upload.SubmissionID, upload.UploadID).Scan(&submissionCount); err != nil {
		return err
	}
	if uint64(submissionCount) >= domain.UploadMaximumSubmissionImages {
		return &domain.QuotaError{Quota: domain.QuotaSubmissionImages, Limit: domain.UploadMaximumSubmissionImages, Observed: uint64(submissionCount) + 1}
	}
	var rollingBytes, storedBytes int64
	var retryAt *time.Time
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(expected_size_bytes), 0), min(quota_charged_at + interval '24 hours')
		FROM devhud_uploads WHERE owner_user_id = $1 AND upload_id <> $2
		AND quota_charged_at > $3::timestamptz - interval '24 hours'`, upload.OwnerUserID, upload.UploadID, now).Scan(&rollingBytes, &retryAt); err != nil {
		return err
	}
	rollingObserved := uint64(rollingBytes) + upload.SizeBytes
	if rollingObserved > domain.UploadMaximumRollingDayBytes {
		return &domain.QuotaError{Quota: domain.QuotaRollingDayBytes, Limit: domain.UploadMaximumRollingDayBytes, Observed: rollingObserved, RetryAt: valueOrZero(retryAt)}
	}
	if err := tx.QueryRow(ctx, `SELECT COALESCE(sum(expected_size_bytes), 0) FROM devhud_uploads
		WHERE owner_user_id = $1 AND upload_id <> $2 AND state IN (2, 3, 4)`, upload.OwnerUserID, upload.UploadID).Scan(&storedBytes); err != nil {
		return err
	}
	storedObserved := uint64(storedBytes) + upload.SizeBytes
	if storedObserved > domain.UploadMaximumStoredBytes {
		return &domain.QuotaError{Quota: domain.QuotaStoredBytes, Limit: domain.UploadMaximumStoredBytes, Observed: storedObserved}
	}
	return nil
}

func (s *Store) CompleteUploadPromotion(ctx context.Context, uploadID, token, publicETag string, now time.Time) (domain.Upload, error) {
	upload, err := scanUpload(s.pool.QueryRow(ctx, `UPDATE devhud_uploads SET state = 3,
		public_etag = $3, finalized_at = COALESCE(finalized_at, $4), operation_token = NULL,
		operation_expires_at = NULL WHERE upload_id = $1 AND state = 2 AND operation_token = $2
		RETURNING `+uploadSelectColumns(), uploadID, token, publicETag, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Upload{}, domain.ErrOperationLeaseLost
	}
	return upload, err
}

func (s *Store) ReleaseUploadPromotion(ctx context.Context, uploadID, token string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devhud_uploads SET state = 1, staging_etag = NULL,
		width = NULL, height = NULL, quota_charged_at = NULL, operation_token = NULL,
		operation_expires_at = NULL WHERE upload_id = $1 AND state = 2 AND operation_token = $2`, uploadID, token)
	return err
}

func (s *Store) RejectUpload(ctx context.Context, ownerID string, binding domain.UploadBinding, failure domain.UploadFailure, now time.Time) error {
	command, err := s.pool.Exec(ctx, `UPDATE devhud_uploads u SET state = 8, removed_at = $8
		FROM devhud_upload_reservations r WHERE u.reservation_id = r.reservation_id
		AND u.upload_id = $1 AND u.owner_user_id = $2 AND u.submission_id = $3
		AND u.upload_group_id = $4 AND u.reservation_id = $5 AND u.staging_generation = $6
		AND u.expected_size_bytes = $7 AND u.state = 1`, binding.UploadID, ownerID, binding.SubmissionID,
		binding.UploadGroupID, binding.ReservationID, int64(binding.StagingGeneration), int64(binding.SizeBytes), now)
	if err != nil {
		return err
	}
	if command.RowsAffected() == 0 && failure != domain.UploadFailureReservationMissing {
		return domain.ErrOperationLeaseLost
	}
	return nil
}

func (s *Store) ListUploads(ctx context.Context, ownerID string, states []domain.UploadState, submissionID string, cursor *domain.UploadCursor, limit uint32) (domain.UploadList, error) {
	if err := s.checkUploadEligible(ctx, ownerID); err != nil {
		return domain.UploadList{}, err
	}
	return s.listUploads(ctx, ownerID, states, submissionID, cursor, limit)
}

func (s *Store) ListUploadsForAdministrator(ctx context.Context, ownerID string, states []domain.UploadState, cursor *domain.UploadCursor, limit uint32) (domain.UploadList, error) {
	return s.listUploads(ctx, ownerID, states, "", cursor, limit)
}

func (s *Store) listUploads(ctx context.Context, ownerID string, states []domain.UploadState, submissionID string, cursor *domain.UploadCursor, limit uint32) (domain.UploadList, error) {
	stateValues := make([]int16, len(states))
	for index, state := range states {
		stateValues[index] = int16(state)
	}
	var cursorTime *time.Time
	var cursorID *string
	if cursor != nil {
		cursorTime, cursorID = &cursor.CreatedAt, &cursor.UploadID
	}
	rows, err := s.pool.Query(ctx, uploadSelect+`
		WHERE u.owner_user_id = $1
		AND (cardinality($2::smallint[]) = 0 OR u.state = ANY($2::smallint[]))
		AND ($3::uuid IS NULL OR u.submission_id = $3)
		AND ($4::timestamptz IS NULL OR (u.created_at, u.upload_id) < ($4, $5::uuid))
		ORDER BY u.created_at DESC, u.upload_id DESC LIMIT $6`, ownerID, stateValues, nullableUUID(submissionID), cursorTime, cursorID, int(limit)+1)
	if err != nil {
		return domain.UploadList{}, err
	}
	defer rows.Close()
	result := domain.UploadList{Uploads: make([]domain.Upload, 0, limit)}
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			return domain.UploadList{}, err
		}
		result.Uploads = append(result.Uploads, upload)
	}
	if err := rows.Err(); err != nil {
		return domain.UploadList{}, err
	}
	if len(result.Uploads) > int(limit) {
		last := result.Uploads[limit-1]
		result.Next = &domain.UploadCursor{CreatedAt: last.CreatedAt, UploadID: last.UploadID}
		result.Uploads = result.Uploads[:limit]
	}
	return result, nil
}

func (s *Store) ClaimUploadRemoval(ctx context.Context, ownerID, uploadID string, reason domain.RemovalReason, token string, now time.Time) (domain.Upload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return domain.Upload{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if ownerID != "" {
		if err := ensureUploadEligible(ctx, tx, ownerID, true); err != nil {
			return domain.Upload{}, err
		}
	}
	query := uploadSelect + ` WHERE u.upload_id = $1 FOR UPDATE OF u`
	upload, err := scanUpload(tx.QueryRow(ctx, query, uploadID))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return domain.Upload{}, domain.ErrNotFound
		}
		return domain.Upload{}, err
	}
	if ownerID != "" && upload.OwnerUserID != ownerID {
		return domain.Upload{}, domain.ErrNotFound
	}
	if upload.State == domain.UploadStateDeleted || upload.State == domain.UploadStateQuarantined {
		return upload, nil
	}
	var operationExpiry *time.Time
	if err := tx.QueryRow(ctx, `SELECT operation_expires_at FROM devhud_uploads WHERE upload_id = $1`, uploadID).Scan(&operationExpiry); err != nil {
		return domain.Upload{}, err
	}
	if upload.State != domain.UploadStatePending && upload.State != domain.UploadStateFinalized && !(upload.State == domain.UploadStateRemoving && (operationExpiry == nil || !now.Before(*operationExpiry))) {
		return domain.Upload{}, &domain.UploadError{Failure: domain.UploadFailureInvalidState}
	}
	upload, err = scanUpload(tx.QueryRow(ctx, `UPDATE devhud_uploads SET state = 4,
		operation_token = $2, operation_expires_at = $3, removal_reason = $4
		WHERE upload_id = $1 RETURNING `+uploadSelectColumns(), uploadID, token, now.Add(domain.UploadOperationLease), int16(reason)))
	if err != nil {
		return domain.Upload{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return domain.Upload{}, err
	}
	return upload, nil
}

func (s *Store) RecordUploadReplacement(ctx context.Context, uploadID, token, replacementETag string) (domain.Upload, error) {
	upload, err := scanUpload(s.pool.QueryRow(ctx, `UPDATE devhud_uploads SET replacement_etag = $3
		WHERE upload_id = $1 AND state = 4 AND operation_token = $2 RETURNING `+uploadSelectColumns(), uploadID, token, replacementETag))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Upload{}, domain.ErrOperationLeaseLost
	}
	return upload, err
}

func (s *Store) CompleteUploadRemoval(ctx context.Context, uploadID, token string, now time.Time) (domain.Upload, error) {
	upload, err := scanUpload(s.pool.QueryRow(ctx, `UPDATE devhud_uploads SET
		state = CASE WHEN removal_reason = 2 THEN 5 ELSE 6 END, removed_at = $3,
		operation_token = NULL, operation_expires_at = NULL
		WHERE upload_id = $1 AND state = 4 AND operation_token = $2 RETURNING `+uploadSelectColumns(), uploadID, token, now))
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Upload{}, domain.ErrOperationLeaseLost
	}
	return upload, err
}

func (s *Store) ReleaseUploadRemoval(ctx context.Context, uploadID, token string) error {
	_, err := s.pool.Exec(ctx, `UPDATE devhud_uploads SET state = CASE WHEN finalized_at IS NULL THEN 1 ELSE 3 END,
		operation_token = NULL, operation_expires_at = NULL, removal_reason = NULL
		WHERE upload_id = $1 AND state = 4 AND operation_token = $2 AND replacement_etag IS NULL`, uploadID, token)
	return err
}

func (s *Store) ClaimExpiredUploads(ctx context.Context, now time.Time, limit int) ([]domain.Upload, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	rows, err := tx.Query(ctx, `WITH candidates AS (
		SELECT u.upload_id FROM devhud_uploads u JOIN devhud_upload_reservations r USING (reservation_id)
		WHERE (u.state IN (1, 3, 5, 6, 7, 8) OR (u.state = 2 AND u.operation_expires_at <= $1))
		AND u.staging_deleted_at IS NULL AND r.staging_expires_at <= $1
		ORDER BY r.staging_expires_at, u.upload_id FOR UPDATE OF u SKIP LOCKED LIMIT $2
	), updated AS (
		UPDATE devhud_uploads u SET state = CASE WHEN u.state IN (1, 2, 8) THEN 7 ELSE u.state END,
		removed_at = CASE WHEN u.state IN (1, 2, 8) THEN $1 ELSE u.removed_at END,
		operation_token = CASE WHEN u.state = 2 THEN NULL ELSE u.operation_token END,
		operation_expires_at = CASE WHEN u.state = 2 THEN NULL ELSE u.operation_expires_at END FROM candidates c
		WHERE u.upload_id = c.upload_id RETURNING u.upload_id
	)
	SELECT `+uploadSelectColumnsWithAliases()+` FROM updated x
	JOIN devhud_uploads u ON u.upload_id = x.upload_id
	JOIN devhud_upload_reservations r ON r.reservation_id = u.reservation_id`, now, limit)
	if err != nil {
		return nil, err
	}
	uploads := make([]domain.Upload, 0, limit)
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		uploads = append(uploads, upload)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return uploads, nil
}

func (s *Store) CompleteExpiredUpload(ctx context.Context, uploadID string, now time.Time) error {
	_, err := s.pool.Exec(ctx, `UPDATE devhud_uploads SET staging_deleted_at = $2 WHERE upload_id = $1`, uploadID, now)
	return err
}

func (s *Store) ListAccountUploadsForPurge(ctx context.Context, ownerID string, limit int) ([]domain.Upload, error) {
	rows, err := s.pool.Query(ctx, uploadSelect+` WHERE u.owner_user_id = $1
		AND (u.state NOT IN (5, 6, 7, 8) OR (u.state IN (5, 6, 7, 8) AND u.staging_deleted_at IS NULL))
		ORDER BY u.created_at LIMIT $2`, ownerID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := make([]domain.Upload, 0, limit)
	for rows.Next() {
		upload, err := scanUpload(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, upload)
	}
	return result, rows.Err()
}

func (s *Store) RemoveAccountUploadMetadata(ctx context.Context, ownerID string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM devhud_submissions WHERE owner_user_id = $1`, ownerID)
	return err
}

func (s *Store) GetUploadUsage(ctx context.Context, ownerID string, now time.Time) (domain.UploadUsage, error) {
	var usage domain.UploadUsage
	var signed, rolling, stored, finalized int64
	err := s.pool.QueryRow(ctx, `SELECT
		(SELECT count(*) FROM devhud_upload_reservations WHERE owner_user_id = $1 AND created_at > $2::timestamptz - interval '1 hour'),
		(SELECT COALESCE(sum(expected_size_bytes), 0) FROM devhud_uploads WHERE owner_user_id = $1 AND quota_charged_at > $2::timestamptz - interval '24 hours'),
		(SELECT COALESCE(sum(expected_size_bytes), 0) FROM devhud_uploads WHERE owner_user_id = $1 AND state IN (2, 3, 4)),
		(SELECT count(*) FROM devhud_uploads WHERE owner_user_id = $1 AND (state IN (2, 3) OR (state = 4 AND finalized_at IS NOT NULL)))`, ownerID, now).Scan(
		&signed, &rolling, &stored, &finalized)
	if err == nil {
		usage.SignedURLsRollingHour, usage.UploadBytesRollingDay = uint64(signed), uint64(rolling)
		usage.StoredBytes, usage.FinalizedImages = uint64(stored), uint64(finalized)
	}
	return usage, err
}

func (s *Store) RecordAdministratorUploadAudit(ctx context.Context, actorID, uploadID string, reason domain.RemovalReason, rationale string, now time.Time) error {
	action := domain.AuditActionUploadDeleted
	if reason == domain.RemovalReasonAdministratorQuarantined {
		action = domain.AuditActionUploadQuarantined
	}
	auditID, err := s.ids.New()
	if err != nil {
		return err
	}
	command, err := s.pool.Exec(ctx, `INSERT INTO devhud_audit_events
		(audit_event_id, actor_user_id, target_user_id, actor_fingerprint, target_fingerprint,
		 action, created_at, expires_at, target_upload_id, reason)
		SELECT $1, actor.user_id, target.user_id, actor.identity_fingerprint,
		 target.identity_fingerprint, $4, $5, $6, u.upload_id, $7
		FROM devhud_users actor, devhud_uploads u
		JOIN devhud_users target ON target.user_id = u.owner_user_id
		WHERE actor.user_id = $2 AND u.upload_id = $3`, auditID, actorID, uploadID, action, now, now.Add(domain.AuditRetention), rationale)
	if err != nil {
		return err
	}
	if command.RowsAffected() != 1 {
		return domain.ErrNotFound
	}
	return nil
}

func (s *Store) checkUploadEligible(ctx context.Context, ownerID string) error {
	var deletion domain.DeletionState
	var block domain.AdministrativeBlockState
	err := s.pool.QueryRow(ctx, `SELECT deletion_state, administrative_block_state FROM devhud_users WHERE user_id = $1`, ownerID).Scan(&deletion, &block)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return uploadEligibility(deletion, block)
}

func ensureUploadEligible(ctx context.Context, tx pgx.Tx, ownerID string, update bool) error {
	lock := "FOR SHARE"
	if update {
		lock = "FOR UPDATE"
	}
	var deletion domain.DeletionState
	var block domain.AdministrativeBlockState
	err := tx.QueryRow(ctx, `SELECT deletion_state, administrative_block_state FROM devhud_users WHERE user_id = $1 `+lock, ownerID).Scan(&deletion, &block)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.ErrNotFound
	}
	if err != nil {
		return err
	}
	return uploadEligibility(deletion, block)
}

func uploadEligibility(deletion domain.DeletionState, block domain.AdministrativeBlockState) error {
	if block == domain.AdministrativeBlockStateBlocked {
		return &domain.PermissionError{Failure: domain.PermissionFailureAdministrativeBlock}
	}
	if deletion != domain.DeletionStateActive {
		return &domain.PermissionError{Failure: domain.PermissionFailureDeletionPending}
	}
	return nil
}

func validateBinding(upload domain.Upload, binding domain.UploadBinding, now time.Time) error {
	if upload.SubmissionID != binding.SubmissionID || upload.UploadGroupID != binding.UploadGroupID || upload.ReservationID != binding.ReservationID || upload.StagingGeneration != binding.StagingGeneration || upload.SizeBytes != binding.SizeBytes || upload.SHA256 != binding.SHA256 {
		return &domain.UploadError{Failure: domain.UploadFailureBindingMismatch}
	}
	if !now.Before(upload.StagingExpiresAt) {
		return &domain.UploadError{Failure: domain.UploadFailureReservationExpired}
	}
	return nil
}

type scanner interface{ Scan(...any) error }

func scanUpload(row scanner) (domain.Upload, error) {
	var upload domain.Upload
	var generation, size int64
	var checksum []byte
	var state int16
	var width, height int32
	var reason int16
	err := row.Scan(&upload.UploadID, &upload.OwnerUserID, &upload.SubmissionID, &upload.UploadGroupID,
		&upload.ReservationID, &upload.PublicID, &upload.StagingID, &generation, &size, &checksum,
		&state, &upload.StagingETag, &upload.PublicETag, &upload.ReplacementETag, &width, &height,
		&upload.CreatedAt, &upload.SignedURLExpiresAt, &upload.StagingExpiresAt, &upload.FinalizedAt,
		&upload.RemovedAt, &reason, &upload.OperationToken)
	if err != nil {
		return domain.Upload{}, err
	}
	if len(checksum) != 32 || generation <= 0 || size < 0 {
		return domain.Upload{}, errors.New("invalid persisted upload metadata")
	}
	copy(upload.SHA256[:], checksum)
	upload.StagingGeneration = uint64(generation)
	upload.SizeBytes = uint64(size)
	upload.State = domain.UploadState(state)
	upload.Width, upload.Height = uint32(width), uint32(height)
	upload.RemovalReason = domain.RemovalReason(reason)
	return upload, nil
}

func uploadSelectColumns() string {
	return `upload_id::text, owner_user_id::text, submission_id::text, upload_group_id::text,
		reservation_id::text, public_id, staging_id, staging_generation, expected_size_bytes,
		expected_sha256, state, COALESCE(staging_etag, ''), COALESCE(public_etag, ''),
		COALESCE(replacement_etag, ''), COALESCE(width, 0), COALESCE(height, 0), created_at,
		(SELECT signed_url_expires_at FROM devhud_upload_reservations WHERE reservation_id = devhud_uploads.reservation_id),
		(SELECT staging_expires_at FROM devhud_upload_reservations WHERE reservation_id = devhud_uploads.reservation_id),
		finalized_at, removed_at, COALESCE(removal_reason, 0), COALESCE(operation_token, '')`
}

func uploadSelectColumnsWithAliases() string {
	return `u.upload_id::text, u.owner_user_id::text, u.submission_id::text, u.upload_group_id::text,
		u.reservation_id::text, u.public_id, u.staging_id, u.staging_generation, u.expected_size_bytes,
		u.expected_sha256, u.state, COALESCE(u.staging_etag, ''), COALESCE(u.public_etag, ''),
		COALESCE(u.replacement_etag, ''), COALESCE(u.width, 0), COALESCE(u.height, 0), u.created_at,
		r.signed_url_expires_at, r.staging_expires_at, u.finalized_at, u.removed_at,
		COALESCE(u.removal_reason, 0), COALESCE(u.operation_token, '')`
}

func nullableUUID(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func valueOrZero(value *time.Time) time.Time {
	if value == nil {
		return time.Time{}
	}
	return *value
}
