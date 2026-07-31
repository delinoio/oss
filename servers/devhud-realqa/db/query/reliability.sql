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

-- name: GetTransactionTimestamp :one
SELECT transaction_timestamp()::timestamptz;

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

-- name: DeleteScopePresetIdempotencySnapshots :execrows
DELETE FROM realqa_idempotency_records AS idempotency
WHERE idempotency.operation = 'create_preset'
  AND idempotency.resource_id IN (
    SELECT preset.id
    FROM realqa_presets AS preset
    WHERE preset.owner_kind = sqlc.arg(scope_owner_kind)
      AND preset.owner_id = sqlc.arg(scope_owner_id)
);

-- name: DeleteScopeSubmissionIdempotencySnapshots :execrows
DELETE FROM realqa_idempotency_records AS idempotency
WHERE (
    idempotency.operation IN (
        'create_submission', 'submit_issue',
        'rebind_submission_storage_authorization',
        'delete_submission_assets'
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
DELETE FROM realqa_submissions AS submission
WHERE submission.owner_kind = sqlc.arg(owner_kind)
  AND submission.owner_id = sqlc.arg(owner_id)
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_authorization_bindings AS binding
      WHERE binding.submission_id = submission.id
  )
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_authorization_attempts AS attempt
      WHERE attempt.submission_id = submission.id
        AND attempt.state = 'pending'
  );

-- Billing-bound submissions and unresolved authorization attempts remain only
-- as pseudonymized resource anchors until exact recovery and closure complete.
-- name: DeleteScopeBillingIssueAttempts :execrows
DELETE FROM realqa_issue_submission_attempts AS attempt
WHERE EXISTS (
    SELECT 1
    FROM realqa_submissions AS submission
    WHERE submission.id = attempt.submission_id
      AND submission.owner_kind = sqlc.arg(owner_kind)
      AND submission.owner_id = sqlc.arg(owner_id)
      AND (
          EXISTS (
              SELECT 1
              FROM realqa_storage_authorization_bindings AS binding
              WHERE binding.submission_id = submission.id
          )
          OR EXISTS (
              SELECT 1
              FROM realqa_storage_authorization_attempts AS storage_attempt
              WHERE storage_attempt.submission_id = submission.id
                AND storage_attempt.state = 'pending'
          )
      )
);

-- name: DeleteScopeBillingAssets :execrows
DELETE FROM realqa_assets AS asset
WHERE EXISTS (
    SELECT 1
    FROM realqa_submissions AS submission
    WHERE submission.id = asset.submission_id
      AND submission.owner_kind = sqlc.arg(owner_kind)
      AND submission.owner_id = sqlc.arg(owner_id)
      AND (
          EXISTS (
              SELECT 1
              FROM realqa_storage_authorization_bindings AS binding
              WHERE binding.submission_id = submission.id
          )
          OR EXISTS (
              SELECT 1
              FROM realqa_storage_authorization_attempts AS storage_attempt
              WHERE storage_attempt.submission_id = submission.id
                AND storage_attempt.state = 'pending'
          )
      )
);

-- name: MinimizeScopeBillingSubmissions :execrows
UPDATE realqa_submissions AS submission
SET preset_id = NULL,
    destination_id = NULL,
    state = 'deleted',
    provider_issue_id = NULL,
    provider_issue_url = NULL,
    declared_encoded_bytes = 0,
    verified_encoded_bytes = 0,
    updated_at = transaction_timestamp(),
    revision = revision + 1
WHERE submission.owner_kind = sqlc.arg(owner_kind)
  AND submission.owner_id = sqlc.arg(owner_id)
  AND EXISTS (
      SELECT 1
      FROM realqa_storage_authorization_bindings AS binding
      WHERE binding.submission_id = submission.id
      UNION ALL
      SELECT 1
      FROM realqa_storage_authorization_attempts AS attempt
      WHERE attempt.submission_id = submission.id
        AND attempt.state = 'pending'
  );

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

-- name: DeleteLifecycleAccountRepositoryAccess :execrows
DELETE FROM realqa_repository_access
WHERE account_id = sqlc.arg(account_id);

-- name: DisconnectGitHubConnectionsForAccount :one
WITH disconnected AS (
    UPDATE realqa_github_connections
    SET state = 'disconnected',
        connected_by_account_id = NULL,
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = revision + 1,
        updated_at = transaction_timestamp()
    WHERE connected_by_account_id = sqlc.arg(account_id)
    RETURNING id
),
cleared_authorizations AS (
    UPDATE realqa_github_user_authorizations
    SET state = 'disconnected',
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = revision + 1,
        updated_at = transaction_timestamp()
    WHERE account_id = sqlc.arg(account_id)
       OR connection_id IN (SELECT id FROM disconnected)
    RETURNING 1
),
cleared_repository_access AS (
    DELETE FROM realqa_repository_access AS access
    USING realqa_github_installations AS installation
    WHERE access.installation_id = installation.id
      AND installation.connection_id IN (SELECT id FROM disconnected)
    RETURNING 1
)
SELECT count(*)::bigint
FROM disconnected;
