-- name: InsertAudit :exec
INSERT INTO realqa_audits (
    id, event_type, actor_reference, owner_kind, owner_id,
    resource_id, decision, result, request_id, trace_id
) VALUES (
    sqlc.arg(id), sqlc.arg(event_type), sqlc.arg(actor_reference),
    sqlc.narg(owner_kind), sqlc.narg(owner_id), sqlc.narg(resource_id),
    sqlc.arg(decision), sqlc.arg(result),
    sqlc.narg(request_id), sqlc.narg(trace_id)
);

-- name: GetDeletionJob :one
SELECT *
FROM realqa_deletion_jobs
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: InsertDeletionJob :one
INSERT INTO realqa_deletion_jobs (
    id, owner_kind, owner_id, trigger_kind, status, already_absent, completed_at
) VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id),
    sqlc.arg(trigger_kind), 'completed', sqlc.arg(already_absent),
    transaction_timestamp()
)
RETURNING *;

-- name: InsertScopeTombstone :exec
INSERT INTO realqa_scope_tombstones (
    owner_kind, owner_id, deletion_job_id, trigger_kind
) VALUES (
    sqlc.arg(owner_kind), sqlc.arg(owner_id),
    sqlc.arg(deletion_job_id), sqlc.arg(trigger_kind)
)
ON CONFLICT (owner_kind, owner_id) DO NOTHING;

-- name: DeleteScopeDisconnectIdempotencySnapshots :execrows
DELETE FROM realqa_idempotency_records AS idempotency
WHERE idempotency.operation = 'disconnect_github_connection'
  AND idempotency.resource_id IN (
    SELECT connection.id
    FROM realqa_github_connections AS connection
    WHERE connection.owner_kind = sqlc.arg(scope_owner_kind)
      AND connection.owner_id = sqlc.arg(scope_owner_id)
);

-- name: DeleteScopeSubmissionIdempotencySnapshots :execrows
DELETE FROM realqa_idempotency_records AS idempotency
WHERE (
    idempotency.operation IN (
        'create_submission', 'delete_submission_assets'
    )
    AND idempotency.resource_id IN (
        SELECT submission.id
        FROM realqa_submissions AS submission
        WHERE submission.owner_kind = sqlc.arg(scope_owner_kind)
          AND submission.owner_id = sqlc.arg(scope_owner_id)
    )
) OR (
    idempotency.operation IN (
        'create_image_upload', 'finalize_image_upload', 'delete_image'
    )
    AND idempotency.resource_id IN (
        SELECT asset.id
        FROM realqa_assets AS asset
        JOIN realqa_submissions AS submission
          ON submission.id = asset.submission_id
        WHERE submission.owner_kind = sqlc.arg(scope_owner_kind)
          AND submission.owner_id = sqlc.arg(scope_owner_id)
    )
);

-- name: DeleteScopePresets :execrows
DELETE FROM realqa_presets
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: DeleteScopeSubmissions :execrows
DELETE FROM realqa_submissions
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: DeleteScopeConnections :execrows
DELETE FROM realqa_github_connections
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: DeleteScopeDestinations :execrows
DELETE FROM realqa_destinations
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: DeleteLifecycleAccountIdentity :execrows
DELETE FROM realqa_identities
WHERE account_id = sqlc.arg(account_id);

-- name: TombstoneLifecycleAccountIdentity :execrows
UPDATE realqa_identities
SET deleted_at = COALESCE(deleted_at, transaction_timestamp())
WHERE account_id = sqlc.arg(account_id);
