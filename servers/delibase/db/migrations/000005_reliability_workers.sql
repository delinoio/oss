ALTER TABLE webhook_inbox
    ADD COLUMN actor_reference text NOT NULL DEFAULT '',
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_at timestamptz,
    ADD COLUMN claim_expires_at timestamptz,
    ADD COLUMN dead_letter_attempt_count integer NOT NULL DEFAULT 0
        CHECK (dead_letter_attempt_count >= 0),
    ADD CONSTRAINT webhook_inbox_actor_reference_check CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    ADD CONSTRAINT webhook_inbox_event_type_check CHECK (event_type IN (
        'order.paid',
        'subscription.created',
        'subscription.updated',
        'subscription.canceled',
        'subscription.revoked',
        'refund.created',
        'refund.updated'
    )),
    ADD CONSTRAINT webhook_inbox_safe_error_class_check CHECK (
        safe_error_class IS NULL
        OR safe_error_class IN (
            'authentication',
            'authorization',
            'invalid_argument',
            'not_found',
            'conflict',
            'rate_limited',
            'dependency',
            'timeout',
            'canceled',
            'internal',
            'worker_crash'
        )
    ),
    ADD CONSTRAINT webhook_inbox_claim_check CHECK (
        (
            claim_token IS NULL
            AND claimed_at IS NULL
            AND claim_expires_at IS NULL
        )
        OR
        (
            claim_token IS NOT NULL
            AND claimed_at IS NOT NULL
            AND claim_expires_at > claimed_at
        )
    ),
    ADD CONSTRAINT webhook_inbox_attempt_limit_check
        CHECK (attempt_count <= 12),
    ADD CONSTRAINT webhook_inbox_dead_letter_check CHECK (
        dead_lettered_at IS NULL
        OR attempt_count = 12
    );

DROP INDEX webhook_inbox_pending_idx;
CREATE INDEX webhook_inbox_pending_idx
    ON webhook_inbox(next_attempt_at, received_at, id)
    WHERE processed_at IS NULL;

CREATE OR REPLACE FUNCTION preserve_webhook_inbox_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'webhook inbox events cannot be deleted'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.provider IS DISTINCT FROM OLD.provider
       OR NEW.provider_event_id IS DISTINCT FROM OLD.provider_event_id
       OR NEW.event_type IS DISTINCT FROM OLD.event_type
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.payload_sha256 IS DISTINCT FROM OLD.payload_sha256
       OR NEW.received_at IS DISTINCT FROM OLD.received_at
       OR NEW.actor_reference IS DISTINCT FROM OLD.actor_reference THEN
        RAISE EXCEPTION 'webhook inbox event is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count
       OR NEW.dead_letter_attempt_count < OLD.dead_letter_attempt_count THEN
        RAISE EXCEPTION 'webhook inbox attempt counts cannot decrease'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.processed_at IS NOT NULL
       AND NEW.processed_at IS DISTINCT FROM OLD.processed_at THEN
        RAISE EXCEPTION 'webhook inbox completion is terminal'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.dead_lettered_at IS NOT NULL
       AND NEW.dead_lettered_at IS DISTINCT FROM OLD.dead_lettered_at THEN
        RAISE EXCEPTION 'webhook inbox dead-letter timestamp is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE integration_outbox
    ADD COLUMN idempotency_key text,
    ADD COLUMN actor_reference text NOT NULL DEFAULT '',
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_at timestamptz,
    ADD COLUMN claim_expires_at timestamptz,
    ADD COLUMN dead_letter_attempt_count integer NOT NULL DEFAULT 0
        CHECK (dead_letter_attempt_count >= 0),
    ADD CONSTRAINT integration_outbox_idempotency_key_check
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    ADD CONSTRAINT integration_outbox_actor_reference_check CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    ADD CONSTRAINT integration_outbox_claim_check CHECK (
        (
            claim_token IS NULL
            AND claimed_at IS NULL
            AND claim_expires_at IS NULL
        )
        OR
        (
            claim_token IS NOT NULL
            AND claimed_at IS NOT NULL
            AND claim_expires_at > claimed_at
        )
    ),
    ADD CONSTRAINT integration_outbox_attempt_limit_check
        CHECK (attempt_count <= 12),
    ADD CONSTRAINT integration_outbox_dead_letter_check CHECK (
        dead_lettered_at IS NULL
        OR attempt_count = 12
    ),
    ADD CONSTRAINT integration_outbox_operation_check CHECK (
        (integration = 'polar' AND operation IN (
            'report_usage',
            'cancel_subscription'
        ))
        OR
        (integration = 'logto' AND operation = 'delete_account')
    ),
    ADD CONSTRAINT integration_outbox_aggregate_check CHECK (
        (
            integration = 'polar'
            AND operation = 'report_usage'
            AND aggregate_type = 'usage_record'
        )
        OR
        (
            integration = 'polar'
            AND operation = 'cancel_subscription'
            AND aggregate_type = 'organization'
        )
        OR
        (
            integration = 'logto'
            AND operation = 'delete_account'
            AND aggregate_type = 'account'
        )
    ),
    ADD CONSTRAINT integration_outbox_safe_error_class_check CHECK (
        safe_error_class IS NULL
        OR safe_error_class IN (
            'authentication',
            'authorization',
            'invalid_argument',
            'not_found',
            'conflict',
            'rate_limited',
            'dependency',
            'timeout',
            'canceled',
            'internal',
            'worker_crash'
        )
    ),
    ADD CONSTRAINT integration_outbox_payload_check CHECK (
        jsonb_typeof(payload) = 'object'
        AND pg_column_size(payload) <= 1048576
    );

UPDATE integration_outbox
SET idempotency_key = 'legacy:' || id::text
WHERE idempotency_key IS NULL;

ALTER TABLE integration_outbox
    ALTER COLUMN idempotency_key SET NOT NULL,
    ADD CONSTRAINT integration_outbox_idempotency_unique
        UNIQUE (integration, operation, idempotency_key);

DROP INDEX integration_outbox_pending_idx;
CREATE INDEX integration_outbox_pending_idx
    ON integration_outbox(next_attempt_at, created_at, id)
    WHERE delivered_at IS NULL;

CREATE OR REPLACE FUNCTION preserve_integration_outbox_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'integration outbox events cannot be deleted'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.integration IS DISTINCT FROM OLD.integration
       OR NEW.operation IS DISTINCT FROM OLD.operation
       OR NEW.aggregate_type IS DISTINCT FROM OLD.aggregate_type
       OR NEW.aggregate_id IS DISTINCT FROM OLD.aggregate_id
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.actor_reference IS DISTINCT FROM OLD.actor_reference THEN
        RAISE EXCEPTION 'integration outbox event is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count
       OR NEW.dead_letter_attempt_count < OLD.dead_letter_attempt_count THEN
        RAISE EXCEPTION 'integration outbox attempt counts cannot decrease'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.delivered_at IS NOT NULL
       AND NEW.delivered_at IS DISTINCT FROM OLD.delivered_at THEN
        RAISE EXCEPTION 'integration outbox delivery is terminal'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.dead_lettered_at IS NOT NULL
       AND NEW.dead_lettered_at IS DISTINCT FROM OLD.dead_lettered_at THEN
        RAISE EXCEPTION 'integration outbox dead-letter timestamp is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE deletion_jobs
    ADD COLUMN idempotency_key text,
    ADD COLUMN actor_reference text NOT NULL DEFAULT '',
    ADD COLUMN claim_token uuid,
    ADD COLUMN claimed_at timestamptz,
    ADD COLUMN claim_expires_at timestamptz,
    ADD COLUMN dead_letter_attempt_count integer NOT NULL DEFAULT 0
        CHECK (dead_letter_attempt_count >= 0),
    ADD CONSTRAINT deletion_jobs_idempotency_key_check
        CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    ADD CONSTRAINT deletion_jobs_actor_reference_check CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    ADD CONSTRAINT deletion_jobs_safe_error_class_check CHECK (
        safe_error_class IS NULL
        OR safe_error_class IN (
            'authentication',
            'authorization',
            'invalid_argument',
            'not_found',
            'conflict',
            'rate_limited',
            'dependency',
            'timeout',
            'canceled',
            'internal',
            'worker_crash'
        )
    ),
    ADD CONSTRAINT deletion_jobs_claim_check CHECK (
        (
            claim_token IS NULL
            AND claimed_at IS NULL
            AND claim_expires_at IS NULL
        )
        OR
        (
            claim_token IS NOT NULL
            AND claimed_at IS NOT NULL
            AND claim_expires_at > claimed_at
        )
    ),
    ADD CONSTRAINT deletion_jobs_attempt_limit_check
        CHECK (attempt_count <= 12),
    ADD CONSTRAINT deletion_jobs_dead_letter_check CHECK (
        dead_lettered_at IS NULL
        OR attempt_count = 12
    );

UPDATE deletion_jobs
SET idempotency_key = 'legacy:' || id::text
WHERE idempotency_key IS NULL;

ALTER TABLE deletion_jobs
    ALTER COLUMN idempotency_key SET NOT NULL,
    ADD CONSTRAINT deletion_jobs_idempotency_unique
        UNIQUE (job_type, idempotency_key);

DROP INDEX deletion_jobs_pending_idx;
CREATE INDEX deletion_jobs_pending_idx
    ON deletion_jobs(next_attempt_at, created_at, id)
    WHERE status IN ('pending', 'processing', 'failed');
CREATE UNIQUE INDEX deletion_jobs_account_target_unique
    ON deletion_jobs(account_id)
    WHERE job_type = 'account';
CREATE UNIQUE INDEX deletion_jobs_organization_target_unique
    ON deletion_jobs(organization_id)
    WHERE job_type = 'organization';

CREATE OR REPLACE FUNCTION preserve_deletion_job_target()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'deletion jobs cannot be deleted'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.job_type IS DISTINCT FROM OLD.job_type
       OR NEW.created_at IS DISTINCT FROM OLD.created_at
       OR NEW.idempotency_key IS DISTINCT FROM OLD.idempotency_key
       OR NEW.actor_reference IS DISTINCT FROM OLD.actor_reference THEN
        RAISE EXCEPTION 'deletion job target is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF NEW.attempt_count < OLD.attempt_count
       OR NEW.dead_letter_attempt_count < OLD.dead_letter_attempt_count THEN
        RAISE EXCEPTION 'deletion job attempt counts cannot decrease'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'completed' AND (
        NEW.status <> 'completed'
        OR NEW.completed_at IS DISTINCT FROM OLD.completed_at
    ) THEN
        RAISE EXCEPTION 'deletion job completion is terminal'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.dead_lettered_at IS NOT NULL
       AND NEW.dead_lettered_at IS DISTINCT FROM OLD.dead_lettered_at THEN
        RAISE EXCEPTION 'deletion job dead-letter timestamp is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

ALTER TABLE audit_events
    ADD CONSTRAINT audit_events_safe_error_class_check CHECK (
        safe_error_class IS NULL
        OR safe_error_class IN (
            'authentication',
            'authorization',
            'invalid_argument',
            'not_found',
            'conflict',
            'rate_limited',
            'dependency',
            'timeout',
            'canceled',
            'internal'
        )
    ),
    ADD CONSTRAINT audit_events_type_check CHECK (event_type IN (
        'authorization.decision',
        'organization.created',
        'organization.updated',
        'organization.deleted',
        'role.updated',
        'invitation.created',
        'invitation.accepted',
        'invitation.revoked',
        'team.created',
        'team.updated',
        'team.deleted',
        'billing_limit.updated',
        'checkout.created',
        'subscription.updated',
        'refund.recorded',
        'reservation.created',
        'reservation.committed',
        'reservation.released',
        'settlement.recorded',
        'account.deletion_requested',
        'organization.deletion_requested',
        'webhook.received',
        'webhook.processed'
    ));
