CREATE TABLE deck_view_viewer_activity (
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    viewer_hash bytea NOT NULL CHECK (octet_length(viewer_hash) = 32),
    last_opened_at timestamptz NOT NULL,
    PRIMARY KEY (view_id, viewer_hash)
);

-- An attempt is advanced only by an authenticated RefreshView request. It is
-- durable for idempotency and billing accounting, not a job or work queue.
CREATE TABLE deck_refresh_attempts (
    subject_hash bytea NOT NULL CHECK (octet_length(subject_hash) = 32),
    refresh_request_id uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    -- Attempts intentionally outlive view deletion so an already-active
    -- request can finish exact dispatch and billing accounting.
    view_id uuid NOT NULL,
    view_revision bigint NOT NULL CHECK (view_revision > 0),
    viewer_hash bytea NOT NULL CHECK (octet_length(viewer_hash) = 32),
    origin smallint NOT NULL CHECK (origin BETWEEN 1 AND 5),
    client_kind smallint NOT NULL CHECK (client_kind BETWEEN 1 AND 4),
    billing_organization_id uuid NOT NULL,
    billing_team_id uuid NOT NULL,
    meter_id uuid NOT NULL,
    price_version_id uuid NOT NULL,
    service_identity_id uuid NOT NULL,
    usd_micros bigint NOT NULL CHECK (usd_micros = 50),
    state smallint NOT NULL CHECK (state BETWEEN 1 AND 4),
    reservation_id uuid,
    provider_dispatched boolean NOT NULL DEFAULT false,
    provider_dispatched_at timestamptz,
    response_ciphertext bytea,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (subject_hash, refresh_request_id),
    CHECK (substring(refresh_request_id::text, 15, 1) = '7'),
    CHECK (substring(billing_organization_id::text, 15, 1) = '7'),
    CHECK (substring(billing_team_id::text, 15, 1) = '7'),
    CHECK (substring(meter_id::text, 15, 1) = '7'),
    CHECK (substring(price_version_id::text, 15, 1) = '7'),
    CHECK (substring(service_identity_id::text, 15, 1) = '7'),
    CHECK (
        reservation_id IS NULL
        OR substring(reservation_id::text, 15, 1) = '7'
    ),
    CHECK (provider_dispatched = (provider_dispatched_at IS NOT NULL)),
    CHECK (
        (state = 4 AND response_ciphertext IS NOT NULL)
        OR state <> 4
    )
);
CREATE INDEX deck_refresh_attempts_viewer_view_idx
    ON deck_refresh_attempts (viewer_hash, view_id, provider_dispatched_at DESC);
CREATE INDEX deck_refresh_attempts_manual_rate_idx
    ON deck_refresh_attempts (subject_hash, created_at DESC)
    WHERE origin = 3;

-- Notification history contains only keyed opaque references and encrypted
-- current-detail material. It is pruned at the exact 30-day boundary by
-- client-owned refresh and notification-resolution requests.
ALTER TABLE deck_notification_events
    ADD COLUMN viewer_hash bytea
        CHECK (viewer_hash IS NULL OR octet_length(viewer_hash) = 32),
    ADD COLUMN repository_hash bytea
        CHECK (repository_hash IS NULL OR octet_length(repository_hash) = 32),
    ADD COLUMN pull_request_number bigint
        CHECK (pull_request_number IS NULL OR pull_request_number > 0),
    ADD COLUMN detail_ciphertext bytea
        CHECK (
            detail_ciphertext IS NULL
            OR octet_length(detail_ciphertext) > 0
        );
CREATE INDEX deck_notification_events_expiry_idx
    ON deck_notification_events (expires_at);
