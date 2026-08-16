CREATE TABLE devhud_users (
    user_id uuid PRIMARY KEY,
    logto_issuer text NOT NULL,
    logto_subject text NOT NULL,
    identity_fingerprint bytea NOT NULL UNIQUE CHECK (octet_length(identity_fingerprint) = 32),
    display_name text NOT NULL DEFAULT '',
    email text NOT NULL DEFAULT '',
    deletion_state smallint NOT NULL DEFAULT 1 CHECK (deletion_state IN (1, 2, 3)),
    administrative_block_state smallint NOT NULL DEFAULT 1 CHECK (administrative_block_state IN (1, 2)),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL,
    deletion_requested_at timestamptz,
    recoverable_until timestamptz,
    CONSTRAINT devhud_users_logto_identity_unique UNIQUE (logto_issuer, logto_subject),
    CONSTRAINT devhud_users_deletion_timestamps_consistent CHECK (
        (deletion_state = 1 AND deletion_requested_at IS NULL AND recoverable_until IS NULL)
        OR (deletion_state IN (2, 3) AND deletion_requested_at IS NOT NULL AND recoverable_until IS NOT NULL)
    )
);

CREATE INDEX devhud_users_recovery_sweep_idx
    ON devhud_users (recoverable_until, user_id)
    WHERE deletion_state IN (2, 3);

CREATE TABLE devhud_settings (
    user_id uuid PRIMARY KEY REFERENCES devhud_users(user_id) ON DELETE CASCADE,
    schema_version bigint NOT NULL CHECK (schema_version BETWEEN 1 AND 4294967295),
    revision numeric(20, 0) NOT NULL CHECK (revision BETWEEN 1 AND 18446744073709551615),
    canonical_json bytea NOT NULL CHECK (octet_length(canonical_json) <= 1048576),
    updated_at timestamptz NOT NULL
);

CREATE TABLE devhud_purged_identities (
    identity_fingerprint bytea PRIMARY KEY CHECK (octet_length(identity_fingerprint) = 32),
    purged_at timestamptz NOT NULL
);

CREATE TABLE devhud_request_logs (
    request_log_id uuid PRIMARY KEY,
    correlation_id uuid NOT NULL,
    procedure text NOT NULL CHECK (octet_length(procedure) <= 256),
    http_status integer NOT NULL CHECK (http_status BETWEEN 100 AND 599),
    duration_milliseconds bigint NOT NULL CHECK (duration_milliseconds >= 0),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT devhud_request_logs_retention CHECK (expires_at <= created_at + interval '30 days')
);

CREATE INDEX devhud_request_logs_expiry_idx ON devhud_request_logs (expires_at);

CREATE TABLE devhud_audit_events (
    audit_event_id uuid PRIMARY KEY,
    actor_user_id uuid REFERENCES devhud_users(user_id) ON DELETE SET NULL,
    target_user_id uuid REFERENCES devhud_users(user_id) ON DELETE SET NULL,
    actor_fingerprint bytea CHECK (actor_fingerprint IS NULL OR octet_length(actor_fingerprint) = 32),
    target_fingerprint bytea CHECK (target_fingerprint IS NULL OR octet_length(target_fingerprint) = 32),
    action smallint NOT NULL CHECK (action BETWEEN 1 AND 4),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CONSTRAINT devhud_audit_events_retention CHECK (expires_at <= created_at + interval '180 days')
);

CREATE INDEX devhud_audit_events_expiry_idx ON devhud_audit_events (expires_at);
