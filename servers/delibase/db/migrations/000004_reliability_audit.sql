CREATE TABLE webhook_inbox (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider = 'polar'),
    provider_event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    payload_sha256 bytea NOT NULL CHECK (octet_length(payload_sha256) = 32),
    received_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    processed_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    dead_lettered_at timestamptz,
    safe_error_class text,
    UNIQUE (provider, provider_event_id),
    CHECK (is_uuid_v7(id)),
    CHECK (jsonb_typeof(payload) = 'object'),
    CHECK (pg_column_size(payload) <= 1048576)
);

CREATE INDEX webhook_inbox_pending_idx
    ON webhook_inbox(next_attempt_at)
    WHERE processed_at IS NULL;

CREATE FUNCTION preserve_webhook_inbox_event()
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
       OR NEW.received_at IS DISTINCT FROM OLD.received_at THEN
        RAISE EXCEPTION 'webhook inbox event is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.processed_at IS NOT NULL AND NEW.processed_at IS NULL THEN
        RAISE EXCEPTION 'webhook inbox processing cannot return to pending'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER webhook_inbox_preserve_event
BEFORE UPDATE OR DELETE ON webhook_inbox
FOR EACH ROW EXECUTE FUNCTION preserve_webhook_inbox_event();

CREATE TABLE integration_outbox (
    id uuid PRIMARY KEY,
    integration text NOT NULL CHECK (integration IN ('polar', 'logto')),
    operation text NOT NULL,
    aggregate_type text NOT NULL,
    aggregate_id uuid NOT NULL,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    delivered_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    dead_lettered_at timestamptz,
    safe_error_class text,
    CHECK (is_uuid_v7(id))
);

CREATE INDEX integration_outbox_pending_idx
    ON integration_outbox(next_attempt_at)
    WHERE delivered_at IS NULL;

CREATE FUNCTION preserve_integration_outbox_event()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.integration IS DISTINCT FROM OLD.integration
       OR NEW.operation IS DISTINCT FROM OLD.operation
       OR NEW.aggregate_type IS DISTINCT FROM OLD.aggregate_type
       OR NEW.aggregate_id IS DISTINCT FROM OLD.aggregate_id
       OR NEW.payload IS DISTINCT FROM OLD.payload
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'integration outbox event is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.delivered_at IS NOT NULL AND NEW.delivered_at IS NULL THEN
        RAISE EXCEPTION 'integration outbox delivery cannot return to pending'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER integration_outbox_preserve_event
BEFORE UPDATE ON integration_outbox
FOR EACH ROW EXECUTE FUNCTION preserve_integration_outbox_event();

CREATE TABLE deletion_jobs (
    id uuid PRIMARY KEY,
    account_id uuid,
    organization_id uuid,
    job_type text NOT NULL CHECK (job_type IN ('account', 'organization')),
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending', 'processing', 'completed', 'failed')),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    dead_lettered_at timestamptz,
    safe_error_class text,
    CHECK (is_uuid_v7(id)),
    CHECK (
        (
            job_type = 'account'
            AND account_id IS NOT NULL
            AND organization_id IS NULL
        )
        OR
        (
            job_type = 'organization'
            AND organization_id IS NOT NULL
            AND account_id IS NULL
        )
    )
);

CREATE INDEX deletion_jobs_pending_idx
    ON deletion_jobs(next_attempt_at)
    WHERE status IN ('pending', 'failed');

CREATE FUNCTION preserve_deletion_job_target()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF NEW.id IS DISTINCT FROM OLD.id
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.organization_id IS DISTINCT FROM OLD.organization_id
       OR NEW.job_type IS DISTINCT FROM OLD.job_type
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'deletion job target is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.status = 'completed' AND NEW.status <> 'completed' THEN
        RAISE EXCEPTION 'deletion job completion is terminal'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.completed_at IS NOT NULL
       AND NEW.completed_at IS DISTINCT FROM OLD.completed_at THEN
        RAISE EXCEPTION 'deletion job completion timestamp is immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER deletion_jobs_preserve_target
BEFORE UPDATE ON deletion_jobs
FOR EACH ROW EXECUTE FUNCTION preserve_deletion_job_target();

CREATE TABLE idempotency_records (
    id uuid PRIMARY KEY,
    caller_kind text NOT NULL CHECK (caller_kind IN ('user', 'service')),
    caller_id text NOT NULL,
    operation text NOT NULL CHECK (operation IN (
        'complete_onboarding',
        'delete_account',
        'create_organization',
        'update_organization',
        'update_organization_slug',
        'delete_organization',
        'update_organization_member_role',
        'remove_organization_member',
        'leave_organization',
        'accept_invitation',
        'revoke_invitation',
        'create_team',
        'update_team',
        'move_team',
        'delete_team_subtree',
        'set_team_membership',
        'remove_team_membership',
        'create_subscription_checkout',
        'create_billing_portal_session',
        'update_overage_limit',
        'reserve_usage',
        'commit_usage',
        'release_usage'
    )),
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    response_payload bytea,
    connect_code integer,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    expires_at timestamptz NOT NULL,
    UNIQUE (caller_kind, caller_id, operation, idempotency_key),
    CHECK (is_uuid_v7(id)),
    CHECK (octet_length(request_hash) = 32),
    CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_records_expiry_idx ON idempotency_records(expires_at);

CREATE FUNCTION preserve_idempotency_record()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'UPDATE' THEN
        RAISE EXCEPTION 'idempotency records are immutable'
            USING ERRCODE = 'check_violation';
    END IF;
    IF OLD.expires_at > transaction_timestamp() THEN
        RAISE EXCEPTION 'unexpired idempotency records cannot be deleted'
            USING ERRCODE = 'check_violation';
    END IF;
    RETURN OLD;
END;
$$;

CREATE TRIGGER idempotency_records_preserve_result
BEFORE UPDATE OR DELETE ON idempotency_records
FOR EACH ROW EXECUTE FUNCTION preserve_idempotency_record();

CREATE FUNCTION audit_metadata_is_safe(value jsonb)
RETURNS boolean
LANGUAGE sql
IMMUTABLE
STRICT
PARALLEL SAFE
AS $$
    SELECT
        jsonb_typeof(value) = 'object'
        AND value - ARRAY[
            'request_id',
            'trace_id',
            'request_method',
            'request_procedure'
        ] = '{}'::jsonb
        AND (
            NOT (value ? 'request_id')
            OR (
                jsonb_typeof(value -> 'request_id') = 'string'
                AND value ->> 'request_id'
                    ~ '^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$'
                AND value ->> 'request_id'
                    !~* '(authorization|token|secret|password|passwd|api[-_]?key|x-delibase-forwarded-user-token):'
                AND value ->> 'request_id'
                    !~ 'eyJ[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}\.[A-Za-z0-9_-]{4,}'
                AND value ->> 'request_id'
                    !~ '([0-9]-?){12,18}[0-9]'
            )
        )
        AND (
            NOT (value ? 'trace_id')
            OR (
                jsonb_typeof(value -> 'trace_id') = 'string'
                AND value ->> 'trace_id' ~ '^[0-9a-f]{32}$'
                AND value ->> 'trace_id' <> repeat('0', 32)
            )
        )
        AND (
            NOT (value ? 'request_method')
            OR (
                jsonb_typeof(value -> 'request_method') = 'string'
                AND value ->> 'request_method' ~ '^[A-Z]{1,16}$'
            )
        )
        AND (
            NOT (value ? 'request_procedure')
            OR (
                jsonb_typeof(value -> 'request_procedure') = 'string'
                AND value ->> 'request_procedure'
                    ~ '^/[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$'
            )
        )
        AND pg_column_size(value) <= 2048
$$;

CREATE TABLE audit_events (
    id uuid PRIMARY KEY,
    occurred_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    event_type text NOT NULL,
    actor_reference text NOT NULL,
    organization_id uuid,
    team_id uuid,
    service_identity_id uuid,
    meter_id uuid,
    reservation_id uuid,
    decision text NOT NULL DEFAULT '' CHECK (decision IN ('', 'allow', 'deny')),
    result text NOT NULL CHECK (result IN ('success', 'failure', 'noop')),
    safe_error_class text,
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb,
    CHECK (is_uuid_v7(id)),
    CHECK (length(event_type) BETWEEN 1 AND 128),
    CHECK (
        actor_reference = ''
        OR actor_reference ~ '^actor:v1:[0-9a-f]{32}$'
    ),
    CHECK (audit_metadata_is_safe(metadata))
);

CREATE INDEX audit_events_organization_idx
    ON audit_events(organization_id, occurred_at, id);
CREATE INDEX audit_events_reservation_idx
    ON audit_events(reservation_id) WHERE reservation_id IS NOT NULL;

CREATE FUNCTION reject_audit_event_mutation()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'audit events are append-only'
        USING ERRCODE = 'check_violation';
END;
$$;

CREATE TRIGGER audit_events_append_only
BEFORE UPDATE OR DELETE ON audit_events
FOR EACH ROW EXECUTE FUNCTION reject_audit_event_mutation();
