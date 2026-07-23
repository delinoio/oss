CREATE TABLE webhook_inbox (
    id uuid PRIMARY KEY,
    provider text NOT NULL CHECK (provider = 'polar'),
    provider_event_id text NOT NULL,
    event_type text NOT NULL,
    payload jsonb NOT NULL,
    received_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    processed_at timestamptz,
    attempt_count integer NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    dead_lettered_at timestamptz,
    safe_error_class text,
    UNIQUE (provider, provider_event_id),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE INDEX webhook_inbox_pending_idx
    ON webhook_inbox(next_attempt_at)
    WHERE processed_at IS NULL;

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
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid)
);

CREATE INDEX integration_outbox_pending_idx
    ON integration_outbox(next_attempt_at)
    WHERE delivered_at IS NULL;

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
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (
        (job_type = 'account' AND account_id IS NOT NULL)
        OR
        (job_type = 'organization' AND organization_id IS NOT NULL)
    )
);

CREATE INDEX deletion_jobs_pending_idx
    ON deletion_jobs(next_attempt_at)
    WHERE status IN ('pending', 'failed');

CREATE TABLE idempotency_records (
    id uuid PRIMARY KEY,
    caller_kind text NOT NULL CHECK (caller_kind IN ('user', 'service')),
    caller_id text NOT NULL,
    operation text NOT NULL,
    idempotency_key text NOT NULL,
    request_hash bytea NOT NULL,
    response_payload bytea,
    connect_code integer,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    expires_at timestamptz NOT NULL,
    UNIQUE (caller_kind, caller_id, operation, idempotency_key),
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (octet_length(request_hash) = 32),
    CHECK (length(idempotency_key) BETWEEN 1 AND 255),
    CHECK (expires_at > created_at)
);

CREATE INDEX idempotency_records_expiry_idx ON idempotency_records(expires_at);

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
    CHECK (id <> '00000000-0000-0000-0000-000000000000'::uuid),
    CHECK (length(event_type) BETWEEN 1 AND 128),
    CHECK (length(actor_reference) <= 255)
);

CREATE INDEX audit_events_organization_idx
    ON audit_events(organization_id, occurred_at, id);
CREATE INDEX audit_events_reservation_idx
    ON audit_events(reservation_id) WHERE reservation_id IS NOT NULL;
