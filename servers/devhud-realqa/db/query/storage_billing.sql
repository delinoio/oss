-- name: CreateStorageAuthorizationBinding :one
INSERT INTO realqa_storage_authorization_bindings (
    authorization_id, submission_id, mapping_revision,
    authorizer_account_id, owner_kind, owner_id, organization_id, team_id,
    service_identity_id, meter_id, maximum_units, status,
    authorization_revision
) VALUES (
    sqlc.arg(authorization_id), sqlc.arg(submission_id),
    sqlc.arg(mapping_revision), sqlc.arg(authorizer_account_id),
    sqlc.arg(owner_kind), sqlc.arg(owner_id), sqlc.arg(organization_id),
    sqlc.arg(team_id), sqlc.arg(service_identity_id), sqlc.arg(meter_id),
    sqlc.arg(maximum_units), sqlc.arg(status),
    sqlc.arg(authorization_revision)
)
ON CONFLICT (authorization_id) DO UPDATE
SET updated_at = transaction_timestamp()
WHERE realqa_storage_authorization_bindings.submission_id =
        EXCLUDED.submission_id
  AND realqa_storage_authorization_bindings.mapping_revision =
        EXCLUDED.mapping_revision
  AND realqa_storage_authorization_bindings.authorizer_account_id =
        EXCLUDED.authorizer_account_id
  AND realqa_storage_authorization_bindings.owner_kind = EXCLUDED.owner_kind
  AND realqa_storage_authorization_bindings.owner_id = EXCLUDED.owner_id
  AND realqa_storage_authorization_bindings.organization_id =
        EXCLUDED.organization_id
  AND realqa_storage_authorization_bindings.team_id = EXCLUDED.team_id
  AND realqa_storage_authorization_bindings.service_identity_id =
        EXCLUDED.service_identity_id
  AND realqa_storage_authorization_bindings.meter_id = EXCLUDED.meter_id
  AND realqa_storage_authorization_bindings.maximum_units =
        EXCLUDED.maximum_units
  AND realqa_storage_authorization_bindings.status = EXCLUDED.status
  AND realqa_storage_authorization_bindings.authorization_revision =
        EXCLUDED.authorization_revision
RETURNING *;

-- name: GetStorageAuthorizationBinding :one
SELECT *
FROM realqa_storage_authorization_bindings
WHERE authorization_id = sqlc.arg(authorization_id);

-- name: GetCurrentStorageAuthorizationBinding :one
SELECT binding.*
FROM realqa_storage_authorization_attempts AS mapping
JOIN realqa_storage_authorization_bindings AS binding
  ON binding.authorization_id = mapping.authorization_id
WHERE mapping.submission_id = sqlc.arg(submission_id)
  AND mapping.mapping_revision = binding.mapping_revision;

-- name: LockCurrentStorageAuthorizationBinding :one
SELECT binding.*
FROM realqa_storage_authorization_attempts AS mapping
JOIN realqa_storage_authorization_bindings AS binding
  ON binding.authorization_id = mapping.authorization_id
WHERE mapping.submission_id = sqlc.arg(submission_id)
  AND mapping.mapping_revision = binding.mapping_revision
FOR UPDATE OF mapping, binding;

-- name: BeginStorageRetention :execrows
INSERT INTO realqa_storage_retention_intervals (
    authorization_id, asset_id, retained_bytes, starts_at
)
SELECT binding.authorization_id,
       asset.id,
       asset.encoded_bytes,
       sqlc.arg(starts_at)
FROM realqa_assets AS asset
JOIN realqa_storage_authorization_attempts AS mapping
  ON mapping.submission_id = asset.submission_id
JOIN realqa_storage_authorization_bindings AS binding
  ON binding.authorization_id = mapping.authorization_id
 AND binding.mapping_revision = mapping.mapping_revision
WHERE asset.id = sqlc.arg(asset_id)
  AND asset.submission_id = sqlc.arg(submission_id)
  AND asset.state = 'public_retained'
  AND asset.upload_state = 'verified'
  AND asset.encoded_bytes > 0
  AND binding.status = 'active'
  AND binding.closure_state = 'open'
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_recoveries AS recovery
      WHERE recovery.submission_id = asset.submission_id
        AND recovery.recovered_at IS NULL
        AND recovery.expired_at IS NULL
  )
ON CONFLICT DO NOTHING;

-- name: BeginRetainedSubmissionStorage :execrows
INSERT INTO realqa_storage_retention_intervals (
    authorization_id, asset_id, retained_bytes, starts_at
)
SELECT binding.authorization_id,
       asset.id,
       asset.encoded_bytes,
       sqlc.arg(starts_at)
FROM realqa_assets AS asset
JOIN realqa_storage_authorization_attempts AS mapping
  ON mapping.submission_id = asset.submission_id
JOIN realqa_storage_authorization_bindings AS binding
  ON binding.authorization_id = mapping.authorization_id
 AND binding.mapping_revision = mapping.mapping_revision
WHERE asset.submission_id = sqlc.arg(submission_id)
  AND asset.state = 'public_retained'
  AND asset.upload_state = 'verified'
  AND asset.encoded_bytes > 0
  AND binding.status = 'active'
  AND binding.closure_state = 'open'
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_recoveries AS recovery
      WHERE recovery.submission_id = asset.submission_id
        AND recovery.recovered_at IS NULL
        AND recovery.expired_at IS NULL
  )
ON CONFLICT DO NOTHING;

-- name: CloseStorageRetentionForAsset :execrows
UPDATE realqa_storage_retention_intervals
SET ends_at = GREATEST(starts_at, sqlc.arg(cutoff))
WHERE asset_id = sqlc.arg(asset_id)
  AND ends_at IS NULL;

-- name: CloseStorageRetentionForSubmission :execrows
UPDATE realqa_storage_retention_intervals AS retained
SET ends_at = GREATEST(
        retained.starts_at,
        LEAST(COALESCE(retained.ends_at, sqlc.arg(cutoff)), sqlc.arg(cutoff))
    )
FROM realqa_storage_authorization_bindings AS binding
WHERE binding.authorization_id = retained.authorization_id
  AND binding.submission_id = sqlc.arg(submission_id)
  AND (
      retained.ends_at IS NULL
      OR retained.ends_at > sqlc.arg(cutoff)
  );

-- name: CloseStorageRetentionForScope :execrows
UPDATE realqa_storage_retention_intervals AS retained
SET ends_at = GREATEST(
        retained.starts_at,
        LEAST(COALESCE(retained.ends_at, sqlc.arg(cutoff)), sqlc.arg(cutoff))
    )
FROM realqa_storage_authorization_bindings AS binding
WHERE binding.authorization_id = retained.authorization_id
  AND binding.owner_kind = sqlc.arg(owner_kind)
  AND binding.owner_id = sqlc.arg(owner_id)
  AND (
      retained.ends_at IS NULL
      OR retained.ends_at > sqlc.arg(cutoff)
  );

-- name: HasStorageSubmissionBlock :one
SELECT EXISTS (
    SELECT 1
    FROM realqa_storage_recoveries AS recovery
    JOIN realqa_submissions AS blocked
      ON blocked.id = recovery.submission_id
    WHERE recovery.recovered_at IS NULL
      AND recovery.expired_at IS NULL
      AND (
          (blocked.owner_kind = sqlc.arg(owner_kind)
              AND blocked.owner_id = sqlc.arg(owner_id))
          OR blocked.payer_organization_id =
              sqlc.arg(payer_organization_id)
      )
);

-- name: GetOldestStorageBillingPeriod :one
WITH periods AS (
    SELECT DISTINCT
           retained.authorization_id,
           generate_series(
               date_trunc(
                   'day', retained.starts_at AT TIME ZONE 'UTC'
               ) AT TIME ZONE 'UTC',
               date_trunc(
                   'day',
                   (
                       LEAST(
                           COALESCE(retained.ends_at, sqlc.arg(cutoff)),
                           sqlc.arg(cutoff)
                       ) - interval '1 microsecond'
                   ) AT TIME ZONE 'UTC'
               ) AT TIME ZONE 'UTC',
               interval '1 day'
           ) AS period_start
    FROM realqa_storage_retention_intervals AS retained
    WHERE retained.starts_at < sqlc.arg(cutoff)
      AND LEAST(
          COALESCE(retained.ends_at, sqlc.arg(cutoff)),
          sqlc.arg(cutoff)
      ) > retained.starts_at
),
unsettled AS (
    SELECT period.authorization_id, period.period_start
    FROM periods AS period
    LEFT JOIN realqa_storage_daily_settlements AS settlement
      ON settlement.authorization_id = period.authorization_id
     AND settlement.period_start = period.period_start
    WHERE settlement.authorization_id IS NULL
       OR settlement.state IN ('pending', 'reserved')
)
SELECT authorization_id,
       to_char(
           period_start AT TIME ZONE 'UTC',
           'YYYY-MM-DD"T"HH24:MI:SS"Z"'
       )::text AS period_start
FROM unsettled
ORDER BY period_start, authorization_id
LIMIT 1;

-- name: CalculateStorageByteSeconds :one
SELECT COALESCE(
    CEIL(SUM(
        retained.retained_bytes::numeric
        * EXTRACT(EPOCH FROM (
            LEAST(
                COALESCE(retained.ends_at, sqlc.arg(period_end)),
                sqlc.arg(period_end)
            )
            - GREATEST(retained.starts_at, sqlc.arg(period_start))
        ))
    )),
    0
)::bigint AS byte_seconds
FROM realqa_storage_retention_intervals AS retained
WHERE retained.authorization_id = sqlc.arg(authorization_id)
  AND retained.starts_at < sqlc.arg(period_end)
  AND COALESCE(retained.ends_at, sqlc.arg(period_end))
        > sqlc.arg(period_start);

-- name: CreateStorageDailySettlement :one
INSERT INTO realqa_storage_daily_settlements (
    authorization_id, period_start, byte_seconds, units, state,
    request_digest, reserve_idempotency_key, commit_idempotency_key,
    release_idempotency_key, settled_at
) VALUES (
    sqlc.arg(authorization_id), sqlc.arg(period_start),
    sqlc.arg(byte_seconds), sqlc.arg(units), sqlc.arg(state),
    sqlc.arg(request_digest), sqlc.arg(reserve_idempotency_key),
    sqlc.arg(commit_idempotency_key), sqlc.arg(release_idempotency_key),
    CASE
        WHEN sqlc.arg(state)::text = 'settled_zero'
            THEN transaction_timestamp()
        ELSE NULL
    END
)
ON CONFLICT (authorization_id, period_start) DO NOTHING
RETURNING *;

-- name: GetStorageDailySettlement :one
SELECT *
FROM realqa_storage_daily_settlements
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start);

-- name: LockStorageDailySettlement :one
SELECT *
FROM realqa_storage_daily_settlements
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
FOR UPDATE;

-- name: SetStorageDailyReservation :one
UPDATE realqa_storage_daily_settlements
SET state = 'reserved',
    reservation_id = sqlc.arg(reservation_id),
    reservation_created_at = sqlc.arg(reservation_created_at),
    reservation_expires_at = sqlc.arg(reservation_expires_at),
    reservation_price_version_id =
        sqlc.arg(reservation_price_version_id),
    attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
  AND state = 'pending'
  AND reservation_id IS NULL
RETURNING *;

-- name: TouchStorageDailySettlementAttempt :exec
UPDATE realqa_storage_daily_settlements
SET attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
  AND state IN ('pending', 'reserved');

-- name: CommitStorageDailySettlement :one
UPDATE realqa_storage_daily_settlements
SET state = 'committed',
    settled_at = transaction_timestamp(),
    attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
  AND reservation_id = sqlc.arg(reservation_id)
  AND state = 'reserved'
RETURNING *;

-- name: ReleaseStorageDailySettlement :one
UPDATE realqa_storage_daily_settlements
SET state = 'released',
    settled_at = transaction_timestamp(),
    attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
  AND reservation_id = sqlc.arg(reservation_id)
  AND state = 'reserved'
RETURNING *;

-- name: SkipStorageDailySettlementForGrace :one
UPDATE realqa_storage_daily_settlements
SET state = 'grace_skipped',
    settled_at = transaction_timestamp(),
    attempt_count = attempt_count + 1,
    last_attempted_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND period_start = sqlc.arg(period_start)
  AND state = 'pending'
  AND reservation_id IS NULL
RETURNING *;

-- name: CreateStorageRecovery :one
INSERT INTO realqa_storage_recoveries (
    id, submission_id, authorization_id, reason,
    grace_started_at, grace_expires_at
) VALUES (
    sqlc.arg(id), sqlc.arg(submission_id), sqlc.arg(authorization_id),
    sqlc.arg(reason), sqlc.arg(grace_started_at),
    sqlc.arg(grace_started_at)::timestamptz + interval '30 days'
)
ON CONFLICT (submission_id)
WHERE recovered_at IS NULL AND expired_at IS NULL DO UPDATE
SET updated_at = transaction_timestamp()
WHERE realqa_storage_recoveries.authorization_id =
        EXCLUDED.authorization_id
RETURNING *;

-- name: ListCurrentStorageBindingsForGitHubConnection :many
SELECT binding.*
FROM realqa_storage_authorization_bindings AS binding
JOIN realqa_storage_authorization_attempts AS current
  ON current.submission_id = binding.submission_id
 AND current.authorization_id = binding.authorization_id
JOIN realqa_submissions AS submission
  ON submission.id = binding.submission_id
JOIN realqa_destinations AS destination
  ON destination.id = submission.destination_id
JOIN realqa_github_installations AS installation
  ON installation.id = destination.installation_id
WHERE installation.connection_id = sqlc.arg(connection_id)
  AND binding.closure_state = 'open'
ORDER BY binding.authorization_id;

-- name: ListOpenStorageBindingsForDisconnectedGitHub :many
SELECT sqlc.embed(binding),
       connection.updated_at AS github_disconnected_at
FROM realqa_storage_authorization_bindings AS binding
JOIN realqa_storage_authorization_attempts AS current
  ON current.submission_id = binding.submission_id
 AND current.authorization_id = binding.authorization_id
JOIN realqa_submissions AS submission
  ON submission.id = binding.submission_id
JOIN realqa_destinations AS destination
  ON destination.id = submission.destination_id
JOIN realqa_github_installations AS installation
  ON installation.id = destination.installation_id
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
WHERE connection.state = 'disconnected'
  AND binding.closure_state = 'open'
  AND binding.accrual_cutoff_at IS NULL
ORDER BY binding.authorization_id
LIMIT sqlc.arg(batch_limit);

-- An account or payer organization may disappear while the feature resource
-- remains owned by somebody else. Those grants enter recovery instead of
-- being silently left chargeable or deleting the other owner's screenshots.
-- name: ListCurrentStorageBindingsForDeletedAuthorizer :many
SELECT binding.*
FROM realqa_storage_authorization_bindings AS binding
JOIN realqa_storage_authorization_attempts AS current
  ON current.submission_id = binding.submission_id
 AND current.authorization_id = binding.authorization_id
WHERE binding.authorizer_account_id = sqlc.arg(account_id)
  AND NOT (
      binding.owner_kind = 'personal'
      AND binding.owner_id = sqlc.arg(account_id)
  )
  AND binding.closure_state = 'open'
  AND binding.accrual_cutoff_at IS NULL
ORDER BY binding.authorization_id;

-- name: ListCurrentStorageBindingsForDeletedPayer :many
SELECT binding.*
FROM realqa_storage_authorization_bindings AS binding
JOIN realqa_storage_authorization_attempts AS current
  ON current.submission_id = binding.submission_id
 AND current.authorization_id = binding.authorization_id
WHERE binding.organization_id = sqlc.arg(organization_id)
  AND NOT (
      binding.owner_kind = 'organization'
      AND binding.owner_id = sqlc.arg(organization_id)
  )
  AND binding.closure_state = 'open'
  AND binding.accrual_cutoff_at IS NULL
ORDER BY binding.authorization_id;

-- name: GetActiveStorageRecovery :one
SELECT *
FROM realqa_storage_recoveries
WHERE submission_id = sqlc.arg(submission_id)
  AND recovered_at IS NULL
  AND expired_at IS NULL;

-- name: MarkStorageRecoveryNotified :one
UPDATE realqa_storage_recoveries
SET notification_state = 'notified',
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND recovered_at IS NULL
  AND expired_at IS NULL
RETURNING *;

-- name: CutoffStorageAuthorizationAccrual :one
UPDATE realqa_storage_authorization_bindings AS binding
SET accrual_cutoff_at = CASE
        WHEN binding.accrual_cutoff_at IS NULL THEN sqlc.arg(cutoff)
        ELSE LEAST(binding.accrual_cutoff_at, sqlc.arg(cutoff))
    END,
    updated_at = transaction_timestamp()
WHERE binding.authorization_id = sqlc.arg(authorization_id)
RETURNING binding.*;

-- name: SetSubmissionStorageBillingGrace :one
UPDATE realqa_submissions AS submission
SET state = CASE
        WHEN submission.state IN (
            'submitted', 'storage_billing_grace'
        ) THEN 'storage_billing_grace'
        ELSE submission.state
    END,
    updated_at = transaction_timestamp(),
    revision = revision + CASE
        WHEN submission.state = 'submitted' THEN 1
        ELSE 0
    END
WHERE submission.id = sqlc.arg(submission_id)
RETURNING submission.*;

-- name: ResolveStorageRecovery :one
WITH recovered AS (
    UPDATE realqa_storage_recoveries
    SET recovered_at = sqlc.arg(recovered_at),
        updated_at = transaction_timestamp()
    WHERE realqa_storage_recoveries.submission_id =
            sqlc.arg(target_submission_id)
      AND realqa_storage_recoveries.recovered_at IS NULL
      AND realqa_storage_recoveries.expired_at IS NULL
    RETURNING realqa_storage_recoveries.submission_id
)
UPDATE realqa_submissions AS submission
SET state = CASE
        WHEN submission.state = 'storage_billing_grace'
          AND EXISTS (
              SELECT 1
              FROM realqa_assets AS asset
              WHERE asset.submission_id = submission.id
                AND asset.state = 'public_retained'
          ) THEN 'submitted'
        WHEN submission.state = 'storage_billing_grace'
          THEN 'assets_deleted'
        ELSE submission.state
    END,
    updated_at = transaction_timestamp(),
    revision = revision + CASE
        WHEN submission.state = 'storage_billing_grace' THEN 1
        ELSE 0
    END
FROM recovered
WHERE submission.id = recovered.submission_id
RETURNING submission.*;

-- name: SupersedeStorageRecovery :one
UPDATE realqa_storage_recoveries
SET recovered_at = sqlc.arg(recovered_at),
    updated_at = transaction_timestamp()
WHERE submission_id = sqlc.arg(submission_id)
  AND authorization_id = sqlc.arg(authorization_id)
  AND reason = 'billing_unavailable'
  AND recovered_at IS NULL
  AND expired_at IS NULL
RETURNING *;

-- name: ListExpiredStorageRecoveries :many
SELECT recovery.*
FROM realqa_storage_recoveries AS recovery
JOIN realqa_submissions AS submission
  ON submission.id = recovery.submission_id
WHERE recovery.recovered_at IS NULL
  AND recovery.expired_at IS NULL
  AND recovery.grace_expires_at <= sqlc.arg(cutoff)
  AND submission.state = 'storage_billing_grace'
ORDER BY recovery.grace_expires_at, recovery.id
LIMIT sqlc.arg(batch_limit);

-- name: MarkStorageRecoveryExpired :one
UPDATE realqa_storage_recoveries
SET expired_at = sqlc.arg(expired_at),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(id)
  AND submission_id = sqlc.arg(submission_id)
  AND grace_expires_at <= sqlc.arg(cutoff)
  AND recovered_at IS NULL
  AND expired_at IS NULL
RETURNING *;

-- name: MarkStorageAuthorizationClosurePending :one
UPDATE realqa_storage_authorization_bindings
SET closure_state = 'resource_deletion_pending',
    accrual_cutoff_at = COALESCE(accrual_cutoff_at, sqlc.arg(cutoff)),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND closure_state IN ('open', 'resource_deletion_pending')
RETURNING *;

-- name: MarkSubmissionStorageClosurePending :execrows
UPDATE realqa_storage_authorization_bindings AS binding
SET closure_state = 'resource_deletion_pending',
    accrual_cutoff_at = COALESCE(binding.accrual_cutoff_at, sqlc.arg(cutoff)),
    updated_at = transaction_timestamp()
WHERE binding.submission_id = sqlc.arg(submission_id)
  AND binding.closure_state IN ('open', 'resource_deletion_pending')
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_assets AS asset
      WHERE asset.submission_id = binding.submission_id
        AND asset.state = 'public_retained'
  );

-- name: MarkScopeStorageClosurePending :execrows
UPDATE realqa_storage_authorization_bindings
SET closure_state = 'resource_deletion_pending',
    closure_owner_deleted_allowed =
        sqlc.arg(owner_deleted_allowed),
    accrual_cutoff_at = COALESCE(accrual_cutoff_at, sqlc.arg(cutoff)),
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id)
  AND closure_state <> 'closed';

-- name: ListStorageClosureCandidates :many
SELECT binding.*
FROM realqa_storage_authorization_bindings AS binding
WHERE binding.closure_state = 'resource_deletion_pending'
  AND binding.accrual_cutoff_at IS NOT NULL
  AND binding.accrual_cutoff_at <= sqlc.arg(completed_through)
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_daily_settlements AS settlement
      WHERE settlement.authorization_id = binding.authorization_id
        AND settlement.state IN ('pending', 'reserved')
  )
  AND NOT EXISTS (
      SELECT 1
      FROM realqa_storage_retention_intervals AS retained
      CROSS JOIN LATERAL generate_series(
          date_trunc(
              'day', retained.starts_at AT TIME ZONE 'UTC'
          ) AT TIME ZONE 'UTC',
          date_trunc(
              'day',
              (
                  LEAST(
                      COALESCE(retained.ends_at, binding.accrual_cutoff_at),
                      binding.accrual_cutoff_at
                  ) - interval '1 microsecond'
              ) AT TIME ZONE 'UTC'
          ) AT TIME ZONE 'UTC',
          interval '1 day'
      ) AS period(period_start)
      LEFT JOIN realqa_storage_daily_settlements AS settlement
        ON settlement.authorization_id = binding.authorization_id
       AND settlement.period_start = period.period_start
      WHERE retained.authorization_id = binding.authorization_id
        AND retained.starts_at < binding.accrual_cutoff_at
        AND LEAST(
            COALESCE(retained.ends_at, binding.accrual_cutoff_at),
            binding.accrual_cutoff_at
        ) > retained.starts_at
        AND (
            settlement.authorization_id IS NULL
            OR settlement.state IN ('pending', 'reserved')
        )
  )
ORDER BY binding.accrual_cutoff_at, binding.authorization_id
LIMIT sqlc.arg(batch_limit);

-- name: CompleteStorageAuthorizationClosure :one
UPDATE realqa_storage_authorization_bindings
SET status = sqlc.arg(status),
    authorization_revision = sqlc.arg(authorization_revision),
    closure_state = 'closed',
    closed_at = transaction_timestamp(),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND closure_state = 'resource_deletion_pending'
RETURNING *;

-- name: UpdateStorageAuthorizationStatus :one
UPDATE realqa_storage_authorization_bindings
SET status = sqlc.arg(status),
    authorization_revision = sqlc.arg(authorization_revision),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND sqlc.arg(authorization_revision) >= authorization_revision
RETURNING *;

-- name: CloseReboundStorageAuthorization :one
UPDATE realqa_storage_authorization_bindings
SET status = sqlc.arg(status),
    authorization_revision = sqlc.arg(authorization_revision),
    closure_state = 'closed',
    closed_at = sqlc.arg(closed_at),
    updated_at = transaction_timestamp()
WHERE authorization_id = sqlc.arg(authorization_id)
  AND closure_state = 'open'
  AND sqlc.arg(status)::text IN ('revoked', 'access_lost')
RETURNING *;

-- name: CreateStorageRebindAttempt :one
INSERT INTO realqa_storage_rebind_attempts (
    submission_id, caller_digest, idempotency_key, request_digest,
    expected_authorization_id, expected_mapping_revision,
    replacement_organization_id, replacement_team_id,
    revoke_idempotency_key, create_idempotency_key, state
) VALUES (
    sqlc.arg(submission_id), sqlc.arg(caller_digest),
    sqlc.arg(idempotency_key), sqlc.arg(request_digest),
    sqlc.arg(expected_authorization_id),
    sqlc.arg(expected_mapping_revision),
    sqlc.arg(replacement_organization_id), sqlc.arg(replacement_team_id),
    sqlc.arg(revoke_idempotency_key), sqlc.arg(create_idempotency_key),
    'pending'
)
ON CONFLICT (caller_digest, idempotency_key) DO NOTHING
RETURNING *;

-- name: GetStorageRebindAttempt :one
SELECT *
FROM realqa_storage_rebind_attempts
WHERE caller_digest = sqlc.arg(caller_digest)
  AND idempotency_key = sqlc.arg(idempotency_key);

-- name: LockStorageRebindAttempt :one
SELECT *
FROM realqa_storage_rebind_attempts
WHERE caller_digest = sqlc.arg(caller_digest)
  AND idempotency_key = sqlc.arg(idempotency_key)
FOR UPDATE;

-- name: ReplaceStorageAuthorizationMapping :one
UPDATE realqa_storage_authorization_attempts
SET authorization_id = sqlc.arg(authorization_id),
    authorization_revision = sqlc.arg(authorization_revision),
    service_identity_id = sqlc.arg(service_identity_id),
    meter_id = sqlc.arg(meter_id),
    maximum_units = sqlc.arg(maximum_units),
    mapping_revision = mapping_revision + 1,
    state = 'active',
    updated_at = transaction_timestamp()
WHERE submission_id = sqlc.arg(submission_id)
  AND authorization_id = sqlc.arg(expected_authorization_id)
  AND mapping_revision = sqlc.arg(expected_mapping_revision)
RETURNING *;

-- name: UpdateSubmissionStoragePayer :one
UPDATE realqa_submissions
SET payer_organization_id = sqlc.arg(organization_id),
    payer_team_id = sqlc.arg(team_id),
    updated_at = transaction_timestamp(),
    revision = revision + 1
WHERE id = sqlc.arg(submission_id)
RETURNING *;

-- name: CompleteStorageRebindAttempt :one
UPDATE realqa_storage_rebind_attempts
SET state = 'completed',
    replacement_authorization_id =
        sqlc.arg(replacement_authorization_id),
    replacement_authorization_revision =
        sqlc.arg(replacement_authorization_revision),
    resulting_mapping_revision = sqlc.arg(resulting_mapping_revision),
    cutoff_at = sqlc.arg(cutoff_at),
    completed_at = transaction_timestamp()
WHERE caller_digest = sqlc.arg(caller_digest)
  AND idempotency_key = sqlc.arg(idempotency_key)
  AND state = 'pending'
RETURNING *;
