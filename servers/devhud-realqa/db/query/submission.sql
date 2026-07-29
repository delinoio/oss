-- name: CountOpenSubmissionsForAccount :one
SELECT count(*)::bigint
FROM realqa_submissions
WHERE created_by_account_id = sqlc.arg(account_id)
  AND state IN ('draft', 'uploading', 'ready')
  AND upload_expires_at > transaction_timestamp();

-- name: CountRecentSubmissionsForAccount :one
SELECT count(*)::bigint
FROM realqa_submissions
WHERE created_by_account_id = sqlc.arg(account_id)
  AND created_at >= transaction_timestamp() - interval '1 hour';

-- name: LockUploadSessionAccount :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(
        'upload-session:' || sqlc.arg(account_id)::uuid::text,
        757
    )
);

-- name: CreateSubmissionRecord :one
INSERT INTO realqa_submissions (
    id, owner_kind, owner_id, created_by_account_id, preset_id,
    destination_id, state, idempotency_digest, payer_organization_id,
    payer_team_id, preset_revision, declared_encoded_bytes,
    upload_deadline, upload_expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id),
    sqlc.arg(created_by_account_id), sqlc.arg(preset_id),
    sqlc.arg(destination_id), 'draft', sqlc.arg(idempotency_digest),
    sqlc.arg(payer_organization_id), sqlc.arg(payer_team_id),
    sqlc.arg(preset_revision), sqlc.arg(declared_encoded_bytes),
    sqlc.arg(upload_deadline), sqlc.arg(upload_expires_at)
)
RETURNING *;

-- name: CreateAssetRecord :one
INSERT INTO realqa_assets (
    id, submission_id, client_image_id, media_type,
    declared_encoded_bytes, pixel_width, pixel_height, source_sha256,
    upload_state, state, encoded_bytes
) VALUES (
    sqlc.arg(id), sqlc.arg(submission_id), sqlc.arg(client_image_id),
    sqlc.arg(media_type), sqlc.arg(declared_encoded_bytes),
    sqlc.arg(pixel_width), sqlc.arg(pixel_height), sqlc.arg(source_sha256),
    'declared', 'private_staging', 0
)
RETURNING *;

-- name: GetSubmissionRecord :one
SELECT *
FROM realqa_submissions
WHERE id = sqlc.arg(id);

-- name: ListSubmissionRecords :many
SELECT submission.*
FROM realqa_submissions AS submission
WHERE submission.owner_kind = sqlc.arg(owner_kind)
  AND submission.owner_id = sqlc.arg(owner_id)
  AND submission.id > sqlc.arg(after_id)
  AND (
      submission.created_by_account_id = sqlc.arg(account_id)
      OR (
        submission.state IN (
            'submitted', 'storage_billing_grace', 'assets_deleted'
        )
        AND EXISTS (
          SELECT 1
          FROM realqa_destinations AS destination
          JOIN realqa_github_installations AS installation
            ON installation.id = destination.installation_id
           AND installation.owner_kind = submission.owner_kind
           AND installation.owner_id = submission.owner_id
          JOIN realqa_github_connections AS connection
            ON connection.id = installation.connection_id
           AND connection.state = 'connected'
          JOIN realqa_repository_access AS access
            ON access.installation_id = destination.installation_id
           AND access.account_id = sqlc.arg(account_id)
           AND access.repository_id = destination.repository_id
           AND access.issues_enabled
           AND access.can_submit
           AND access.checked_at >=
               statement_timestamp() - interval '5 minutes'
          WHERE destination.id = submission.destination_id
            AND destination.owner_kind = submission.owner_kind
            AND destination.owner_id = submission.owner_id
        )
      )
  )
ORDER BY submission.id
LIMIT sqlc.arg(page_limit);

-- name: LockSubmissionRecord :one
SELECT *
FROM realqa_submissions
WHERE id = sqlc.arg(submission_record_id)
FOR UPDATE;

-- name: ListSubmissionAssets :many
SELECT *
FROM realqa_assets
WHERE submission_id = sqlc.arg(submission_id)
ORDER BY id;

-- name: GetAssetRecord :one
SELECT *
FROM realqa_assets
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id);

-- name: LockAssetRecord :one
SELECT *
FROM realqa_assets
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
FOR UPDATE;

-- name: AuthorizeAssetUpload :one
UPDATE realqa_assets
SET upload_state = 'put_authorized',
    upload_token_digest = sqlc.arg(upload_token_digest),
    upload_expires_at = sqlc.arg(upload_expires_at),
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND revision = sqlc.arg(expected_revision)
  AND upload_state IN ('declared', 'put_authorized')
RETURNING *;

-- name: GetAssetUploadGrant :one
SELECT asset.*, submission.upload_deadline
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
WHERE asset.upload_token_digest = sqlc.arg(upload_token_digest)
  AND asset.upload_state IN ('put_authorized', 'uploaded');

-- name: MarkAssetUploaded :one
UPDATE realqa_assets
SET upload_state = 'uploaded',
    uploaded_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND upload_token_digest = sqlc.arg(upload_token_digest)
  AND upload_state = 'put_authorized'
RETURNING *;

-- name: MarkAssetVerifying :one
UPDATE realqa_assets
SET upload_state = 'verifying',
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND revision = sqlc.arg(expected_revision)
  AND upload_state = 'uploaded'
RETURNING *;

-- name: SumOtherVerifiedAssetBytes :one
SELECT COALESCE(sum(encoded_bytes), 0)::bigint
FROM realqa_assets
WHERE submission_id = sqlc.arg(submission_id)
  AND id <> sqlc.arg(asset_id)
  AND upload_state = 'verified'
  AND state IN ('verified_unlinked', 'public_retained');

-- name: MarkAssetVerified :one
UPDATE realqa_assets
SET upload_state = 'verified',
    state = 'verified_unlinked',
    encoded_bytes = sqlc.arg(encoded_bytes),
    sanitized_sha256 = sqlc.arg(sanitized_sha256),
    verified_at = transaction_timestamp(),
    upload_token_digest = NULL,
    upload_expires_at = NULL,
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND upload_state = 'verifying'
RETURNING *;

-- name: MarkAssetRejected :one
UPDATE realqa_assets
SET upload_state = 'rejected',
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND upload_state IN ('uploaded', 'verifying')
RETURNING *;

-- name: UpdateSubmissionVerifiedBytes :one
UPDATE realqa_submissions AS submission
SET verified_encoded_bytes = sqlc.arg(verified_encoded_bytes),
    state = CASE
        WHEN EXISTS (
            SELECT 1 FROM realqa_assets
            WHERE submission_id = submission.id
              AND upload_state = 'verified'
        ) AND NOT EXISTS (
            SELECT 1 FROM realqa_assets
            WHERE submission_id = submission.id
              AND upload_state NOT IN (
                  'verified', 'rejected', 'expired', 'deleted'
              )
        ) THEN 'ready'
        ELSE 'uploading'
    END,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE submission.id = sqlc.arg(submission_record_id)
  AND submission.state IN ('draft', 'uploading', 'ready')
RETURNING *;

-- name: PromoteAsset :one
UPDATE realqa_assets
SET public_id = sqlc.arg(public_id),
    state = 'public_retained',
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND upload_state = 'verified'
  AND state = 'verified_unlinked'
RETURNING *;

-- name: MarkSubmissionSubmitted :one
UPDATE realqa_submissions
SET state = 'submitted',
    submitted_at = COALESCE(submitted_at, transaction_timestamp()),
    updated_at = transaction_timestamp(),
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND state IN ('ready', 'submitting', 'reconciling', 'submitted')
RETURNING *;

-- name: GetPublicAsset :one
SELECT asset.public_id, asset.media_type, asset.state
FROM realqa_assets AS asset
WHERE asset.public_id = sqlc.arg(public_lookup_id)
UNION ALL
SELECT tombstone.public_id, ''::text AS media_type,
       'removed_placeholder'::text AS state
FROM realqa_public_asset_tombstones AS tombstone
WHERE tombstone.public_id = sqlc.arg(public_lookup_id)
LIMIT 1;

-- name: TombstoneAsset :one
WITH selected AS (
    SELECT selected_asset.public_id
    FROM realqa_assets AS selected_asset
    WHERE selected_asset.id = sqlc.arg(asset_record_id)
      AND selected_asset.submission_id = sqlc.arg(asset_submission_id)
      AND selected_asset.revision = sqlc.arg(expected_revision)
    FOR UPDATE
), tombstone AS (
    INSERT INTO realqa_public_asset_tombstones (public_id)
    SELECT public_id FROM selected WHERE public_id IS NOT NULL
    ON CONFLICT (public_id) DO NOTHING
)
UPDATE realqa_assets AS asset
SET upload_state = 'deleted',
    state = CASE
        WHEN asset.public_id IS NULL THEN 'deleted'
        ELSE 'removed_placeholder'
    END,
    removed_at = COALESCE(asset.removed_at, transaction_timestamp()),
    revision = asset.revision + 1
WHERE asset.id = sqlc.arg(asset_record_id)
  AND asset.submission_id = sqlc.arg(asset_submission_id)
  AND asset.revision = sqlc.arg(expected_revision)
RETURNING asset.*;

-- name: ListRemovableSubmissionAssets :many
SELECT *
FROM realqa_assets
WHERE submission_id = sqlc.arg(submission_id)
  AND state NOT IN ('removed_placeholder', 'deleted', 'expired')
ORDER BY id
FOR UPDATE;

-- name: TombstoneSubmissionAssets :many
WITH selected AS (
    SELECT selected_asset.id, selected_asset.public_id
    FROM realqa_assets AS selected_asset
    WHERE selected_asset.submission_id = sqlc.arg(asset_submission_id)
      AND selected_asset.state NOT IN ('removed_placeholder', 'deleted', 'expired')
    FOR UPDATE
), tombstones AS (
    INSERT INTO realqa_public_asset_tombstones (public_id)
    SELECT public_id FROM selected WHERE public_id IS NOT NULL
    ON CONFLICT (public_id) DO NOTHING
)
UPDATE realqa_assets AS asset
SET upload_state = 'deleted',
    state = CASE
        WHEN asset.public_id IS NULL THEN 'deleted'
        ELSE 'removed_placeholder'
    END,
    removed_at = COALESCE(asset.removed_at, transaction_timestamp()),
    revision = asset.revision + 1
FROM selected
WHERE asset.id = selected.id
RETURNING asset.*;

-- name: MarkSubmissionAssetsDeleted :one
UPDATE realqa_submissions
SET state = 'assets_deleted',
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: TouchSubmissionAfterAssetDeletion :one
UPDATE realqa_submissions AS submission
SET verified_encoded_bytes = (
        SELECT COALESCE(sum(encoded_bytes), 0)::bigint
        FROM realqa_assets
        WHERE submission_id = submission.id
          AND upload_state = 'verified'
          AND state IN ('verified_unlinked', 'public_retained')
    ),
    state = CASE
        WHEN submission.state IN ('draft', 'uploading', 'ready')
             AND NOT EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state NOT IN (
                       'rejected', 'expired', 'deleted'
                   )
             ) THEN 'assets_deleted'
        WHEN submission.state IN ('draft', 'uploading', 'ready')
             AND EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state = 'verified'
             )
             AND NOT EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state NOT IN (
                       'verified', 'rejected', 'expired', 'deleted'
                   )
             ) THEN 'ready'
        WHEN submission.state IN ('draft', 'uploading', 'ready')
            THEN 'uploading'
        ELSE submission.state
    END,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE submission.id = sqlc.arg(id)
  AND submission.revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: RefreshSubmissionAssetState :one
UPDATE realqa_submissions AS submission
SET verified_encoded_bytes = (
        SELECT COALESCE(sum(encoded_bytes), 0)::bigint
        FROM realqa_assets
        WHERE submission_id = submission.id
          AND upload_state = 'verified'
          AND state IN ('verified_unlinked', 'public_retained')
    ),
    state = CASE
        WHEN NOT EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state NOT IN (
                       'rejected', 'expired', 'deleted'
                   )
             ) THEN 'assets_deleted'
        WHEN EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state = 'verified'
             )
             AND NOT EXISTS (
                 SELECT 1 FROM realqa_assets
                 WHERE submission_id = submission.id
                   AND upload_state NOT IN (
                       'verified', 'rejected', 'expired', 'deleted'
                   )
             ) THEN 'ready'
        ELSE 'uploading'
    END,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE submission.id = sqlc.arg(id)
  AND submission.state IN ('draft', 'uploading', 'ready')
RETURNING *;

-- name: ListIssueAssets :many
SELECT asset.*
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
WHERE submission.provider_issue_id = sqlc.arg(provider_issue_id)
  AND asset.state NOT IN ('removed_placeholder', 'deleted', 'expired')
FOR UPDATE OF asset;

-- name: ListExpiredPrivateAssets :many
SELECT asset.*
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
WHERE asset.state IN ('private_staging', 'verified_unlinked')
  AND submission.upload_expires_at <= sqlc.arg(cutoff)
ORDER BY asset.created_at
LIMIT sqlc.arg(batch_limit)
FOR UPDATE OF asset SKIP LOCKED;

-- name: ExpireAsset :one
UPDATE realqa_assets
SET upload_state = 'expired',
    state = 'expired',
    removed_at = COALESCE(removed_at, transaction_timestamp()),
    upload_token_digest = NULL,
    upload_expires_at = NULL,
    revision = revision + 1
WHERE id = sqlc.arg(id)
  AND state IN ('private_staging', 'verified_unlinked')
RETURNING *;

-- name: ListScopeObjectAssets :many
SELECT asset.*
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
WHERE submission.owner_kind = sqlc.arg(owner_kind)
  AND submission.owner_id = sqlc.arg(owner_id)
FOR UPDATE OF asset;

-- name: TombstoneScopePublicAssets :exec
INSERT INTO realqa_public_asset_tombstones (public_id)
SELECT asset.public_id
FROM realqa_assets AS asset
JOIN realqa_submissions AS submission ON submission.id = asset.submission_id
WHERE submission.owner_kind = sqlc.arg(owner_kind)
  AND submission.owner_id = sqlc.arg(owner_id)
  AND asset.public_id IS NOT NULL
ON CONFLICT (public_id) DO NOTHING;

-- name: EnqueueObjectDeletion :exec
INSERT INTO realqa_object_deletion_jobs (
    asset_id, object_kind, public_id
) VALUES (
    sqlc.arg(asset_id), sqlc.arg(object_kind), sqlc.narg(public_id)
)
ON CONFLICT DO NOTHING;

-- name: ListPendingObjectDeletions :many
SELECT *
FROM realqa_object_deletion_jobs
WHERE next_attempt_at <= sqlc.arg(cutoff)
ORDER BY next_attempt_at, created_at
LIMIT sqlc.arg(batch_limit);

-- name: RetryObjectDeletion :exec
UPDATE realqa_object_deletion_jobs
SET attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    next_attempt_at = transaction_timestamp() + interval '5 minutes'
WHERE asset_id = sqlc.arg(asset_id)
  AND object_kind = sqlc.arg(object_kind)
  AND public_id IS NOT DISTINCT FROM sqlc.narg(public_id);

-- name: CompleteObjectDeletion :exec
DELETE FROM realqa_object_deletion_jobs
WHERE asset_id = sqlc.arg(asset_id)
  AND object_kind = sqlc.arg(object_kind)
  AND public_id IS NOT DISTINCT FROM sqlc.narg(public_id);
