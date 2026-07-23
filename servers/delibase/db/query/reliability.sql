-- name: EnqueueWebhookInbox :one
INSERT INTO webhook_inbox (
    id,
    provider,
    provider_event_id,
    event_type,
    payload,
    payload_sha256,
    actor_reference
) VALUES (
    sqlc.arg(id),
    sqlc.arg(provider),
    sqlc.arg(provider_event_id),
    sqlc.arg(event_type),
    sqlc.arg(payload),
    sqlc.arg(payload_sha256),
    sqlc.arg(actor_reference)
)
ON CONFLICT (provider, provider_event_id) DO UPDATE
SET provider_event_id = EXCLUDED.provider_event_id
WHERE webhook_inbox.event_type = EXCLUDED.event_type
  AND webhook_inbox.payload_sha256 = EXCLUDED.payload_sha256
  AND webhook_inbox.actor_reference = EXCLUDED.actor_reference
RETURNING *;

-- name: EnqueueIntegrationOutbox :one
INSERT INTO integration_outbox (
    id,
    integration,
    operation,
    aggregate_type,
    aggregate_id,
    payload,
    idempotency_key,
    actor_reference
) VALUES (
    sqlc.arg(id),
    sqlc.arg(integration),
    sqlc.arg(operation),
    sqlc.arg(aggregate_type),
    sqlc.arg(aggregate_id),
    sqlc.arg(payload),
    sqlc.arg(idempotency_key),
    sqlc.arg(actor_reference)
)
ON CONFLICT (integration, operation, idempotency_key) DO UPDATE
SET idempotency_key = EXCLUDED.idempotency_key
WHERE integration_outbox.aggregate_type = EXCLUDED.aggregate_type
  AND integration_outbox.aggregate_id = EXCLUDED.aggregate_id
  AND integration_outbox.payload = EXCLUDED.payload
  AND integration_outbox.actor_reference = EXCLUDED.actor_reference
RETURNING *;

-- name: EnqueueDeletionJob :one
INSERT INTO deletion_jobs (
    id,
    account_id,
    organization_id,
    job_type,
    idempotency_key,
    actor_reference
) VALUES (
    sqlc.arg(id),
    sqlc.narg(account_id),
    sqlc.narg(organization_id),
    sqlc.arg(job_type),
    sqlc.arg(idempotency_key),
    sqlc.arg(actor_reference)
)
ON CONFLICT (job_type, idempotency_key) DO UPDATE
SET idempotency_key = EXCLUDED.idempotency_key
WHERE deletion_jobs.account_id IS NOT DISTINCT FROM EXCLUDED.account_id
  AND deletion_jobs.organization_id IS NOT DISTINCT FROM EXCLUDED.organization_id
  AND deletion_jobs.actor_reference = EXCLUDED.actor_reference
RETURNING *;

-- name: AppendAuditEvent :one
INSERT INTO audit_events (
    id,
    occurred_at,
    event_type,
    actor_reference,
    organization_id,
    team_id,
    service_identity_id,
    meter_id,
    reservation_id,
    decision,
    result,
    safe_error_class,
    metadata
) VALUES (
    sqlc.arg(id),
    sqlc.arg(occurred_at),
    sqlc.arg(event_type),
    sqlc.arg(actor_reference),
    sqlc.narg(organization_id),
    sqlc.narg(team_id),
    sqlc.narg(service_identity_id),
    sqlc.narg(meter_id),
    sqlc.narg(reservation_id),
    sqlc.arg(decision),
    sqlc.arg(result),
    sqlc.narg(safe_error_class),
    sqlc.arg(metadata)
)
RETURNING *;

-- name: ClaimWebhookInbox :one
WITH candidate AS (
    SELECT id
    FROM webhook_inbox
    WHERE processed_at IS NULL
      AND next_attempt_at <= sqlc.arg(now)
      AND (
          (
              dead_lettered_at IS NULL
              AND attempt_count < 12
              AND (claim_token IS NULL OR claim_expires_at <= sqlc.arg(now))
          )
          OR (dead_lettered_at IS NOT NULL AND claim_token IS NULL)
      )
    ORDER BY next_attempt_at, received_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE webhook_inbox AS event
SET claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(now),
    claim_expires_at = sqlc.arg(claim_expires_at),
    attempt_count = event.attempt_count
        + CASE WHEN event.dead_lettered_at IS NULL THEN 1 ELSE 0 END,
    dead_letter_attempt_count = event.dead_letter_attempt_count
        + CASE WHEN event.dead_lettered_at IS NULL THEN 0 ELSE 1 END
FROM candidate
WHERE event.id = candidate.id
RETURNING event.*;

-- name: CompleteWebhookInbox :one
UPDATE webhook_inbox
SET processed_at = sqlc.arg(completed_at),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL,
    safe_error_class = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND processed_at IS NULL
RETURNING id;

-- name: FailWebhookInbox :one
UPDATE webhook_inbox
SET next_attempt_at = sqlc.arg(next_attempt_at),
    dead_lettered_at = CASE
        WHEN sqlc.arg(dead_letter)::boolean
            THEN COALESCE(dead_lettered_at, sqlc.arg(failed_at))
        ELSE dead_lettered_at
    END,
    safe_error_class = sqlc.arg(safe_error_class),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND processed_at IS NULL
RETURNING id;

-- name: RecoverExhaustedWebhookInbox :execrows
UPDATE webhook_inbox
SET dead_lettered_at = COALESCE(dead_lettered_at, claim_expires_at),
    next_attempt_at = claim_expires_at + interval '24 hours',
    safe_error_class = 'worker_crash',
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE processed_at IS NULL
  AND attempt_count = 12
  AND claim_expires_at <= sqlc.arg(now);

-- name: ClaimIntegrationOutbox :one
WITH candidate AS (
    SELECT id
    FROM integration_outbox
    WHERE delivered_at IS NULL
      AND next_attempt_at <= sqlc.arg(now)
      AND (
          (
              dead_lettered_at IS NULL
              AND attempt_count < 12
              AND (claim_token IS NULL OR claim_expires_at <= sqlc.arg(now))
          )
          OR (dead_lettered_at IS NOT NULL AND claim_token IS NULL)
      )
    ORDER BY next_attempt_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE integration_outbox AS event
SET claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(now),
    claim_expires_at = sqlc.arg(claim_expires_at),
    attempt_count = event.attempt_count
        + CASE WHEN event.dead_lettered_at IS NULL THEN 1 ELSE 0 END,
    dead_letter_attempt_count = event.dead_letter_attempt_count
        + CASE WHEN event.dead_lettered_at IS NULL THEN 0 ELSE 1 END
FROM candidate
WHERE event.id = candidate.id
RETURNING event.*;

-- name: CompleteIntegrationOutbox :one
UPDATE integration_outbox
SET delivered_at = sqlc.arg(completed_at),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL,
    safe_error_class = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND delivered_at IS NULL
RETURNING id;

-- name: FailIntegrationOutbox :one
UPDATE integration_outbox
SET next_attempt_at = sqlc.arg(next_attempt_at),
    dead_lettered_at = CASE
        WHEN sqlc.arg(dead_letter)::boolean
            THEN COALESCE(dead_lettered_at, sqlc.arg(failed_at))
        ELSE dead_lettered_at
    END,
    safe_error_class = sqlc.arg(safe_error_class),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND delivered_at IS NULL
RETURNING id;

-- name: RecoverExhaustedIntegrationOutbox :execrows
UPDATE integration_outbox
SET dead_lettered_at = COALESCE(dead_lettered_at, claim_expires_at),
    next_attempt_at = claim_expires_at + interval '24 hours',
    safe_error_class = 'worker_crash',
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE delivered_at IS NULL
  AND attempt_count = 12
  AND claim_expires_at <= sqlc.arg(now);

-- name: ClaimDeletionJob :one
WITH candidate AS (
    SELECT id
    FROM deletion_jobs
    WHERE status IN ('pending', 'processing', 'failed')
      AND next_attempt_at <= sqlc.arg(now)
      AND (
          (
              dead_lettered_at IS NULL
              AND attempt_count < 12
              AND (claim_token IS NULL OR claim_expires_at <= sqlc.arg(now))
          )
          OR (dead_lettered_at IS NOT NULL AND claim_token IS NULL)
      )
    ORDER BY next_attempt_at, created_at, id
    FOR UPDATE SKIP LOCKED
    LIMIT 1
)
UPDATE deletion_jobs AS job
SET status = 'processing',
    claim_token = sqlc.arg(claim_token),
    claimed_at = sqlc.arg(now),
    claim_expires_at = sqlc.arg(claim_expires_at),
    attempt_count = job.attempt_count
        + CASE WHEN job.dead_lettered_at IS NULL THEN 1 ELSE 0 END,
    dead_letter_attempt_count = job.dead_letter_attempt_count
        + CASE WHEN job.dead_lettered_at IS NULL THEN 0 ELSE 1 END
FROM candidate
WHERE job.id = candidate.id
RETURNING job.*;

-- name: CompleteDeletionJob :one
UPDATE deletion_jobs
SET status = 'completed',
    completed_at = sqlc.arg(completed_at),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL,
    safe_error_class = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing'
RETURNING id;

-- name: FailDeletionJob :one
UPDATE deletion_jobs
SET status = 'failed',
    next_attempt_at = sqlc.arg(next_attempt_at),
    dead_lettered_at = CASE
        WHEN sqlc.arg(dead_letter)::boolean
            THEN COALESCE(dead_lettered_at, sqlc.arg(failed_at))
        ELSE dead_lettered_at
    END,
    safe_error_class = sqlc.arg(safe_error_class),
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE id = sqlc.arg(id)
  AND claim_token = sqlc.arg(claim_token)
  AND status = 'processing'
RETURNING id;

-- name: RecoverExhaustedDeletionJobs :execrows
UPDATE deletion_jobs
SET status = 'failed',
    dead_lettered_at = COALESCE(dead_lettered_at, claim_expires_at),
    next_attempt_at = claim_expires_at + interval '24 hours',
    safe_error_class = 'worker_crash',
    claim_token = NULL,
    claimed_at = NULL,
    claim_expires_at = NULL
WHERE status = 'processing'
  AND attempt_count = 12
  AND claim_expires_at <= sqlc.arg(now);

-- name: GetWebhookInbox :one
SELECT * FROM webhook_inbox WHERE id = $1;

-- name: GetIntegrationOutbox :one
SELECT * FROM integration_outbox WHERE id = $1;

-- name: GetDeletionJob :one
SELECT * FROM deletion_jobs WHERE id = $1;

-- name: GetAuditEvent :one
SELECT * FROM audit_events WHERE id = $1;
