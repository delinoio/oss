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
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    viewer_hash bytea NOT NULL CHECK (octet_length(viewer_hash) = 32),
    origin smallint NOT NULL CHECK (origin BETWEEN 1 AND 5),
    client_kind smallint NOT NULL CHECK (client_kind BETWEEN 1 AND 4),
    state smallint NOT NULL CHECK (state BETWEEN 1 AND 4),
    reservation_id uuid,
    provider_dispatched boolean NOT NULL DEFAULT false,
    response_ciphertext bytea,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (subject_hash, refresh_request_id),
    CHECK (substring(refresh_request_id::text, 15, 1) = '7'),
    CHECK (
        reservation_id IS NULL
        OR substring(reservation_id::text, 15, 1) = '7'
    ),
    CHECK (
        (state = 4 AND response_ciphertext IS NOT NULL)
        OR state <> 4
    )
);
CREATE INDEX deck_refresh_attempts_viewer_view_idx
    ON deck_refresh_attempts (viewer_hash, view_id, created_at DESC);

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
