ALTER TABLE deck_connections
    ADD COLUMN github_installation_id bigint,
    ADD COLUMN github_account_id bigint,
    ADD COLUMN github_account_kind smallint,
    ADD COLUMN github_account_login_ciphertext bytea,
    ADD COLUMN github_metadata_permission smallint,
    ADD COLUMN github_contents_permission smallint,
    ADD COLUMN github_pull_requests_permission smallint,
    ADD COLUMN github_checks_permission smallint,
    ADD COLUMN github_members_permission smallint,
    ADD CONSTRAINT deck_connections_installation_unique
        UNIQUE (github_installation_id),
    ADD CONSTRAINT deck_connections_github_values_check CHECK (
        (github_installation_id IS NULL
            AND github_account_id IS NULL
            AND github_account_kind IS NULL
            AND github_account_login_ciphertext IS NULL
            AND github_metadata_permission IS NULL
            AND github_contents_permission IS NULL
            AND github_pull_requests_permission IS NULL
            AND github_checks_permission IS NULL
            AND github_members_permission IS NULL)
        OR
        (github_installation_id > 0
            AND github_account_id > 0
            AND github_account_kind IN (1, 2)
            AND octet_length(github_account_login_ciphertext) > 0
            AND github_metadata_permission BETWEEN 0 AND 3
            AND github_contents_permission BETWEEN 0 AND 3
            AND github_pull_requests_permission BETWEEN 0 AND 3
            AND github_checks_permission BETWEEN 0 AND 3
            AND github_members_permission BETWEEN 0 AND 3)
    );

-- Snapshot rows are a replaceable cache. Purge pre-index rows because their
-- encrypted repository references cannot be safely backfilled without opening
-- identity-bearing data during migration.
DELETE FROM deck_pull_request_snapshots;
DELETE FROM deck_pull_request_snapshot_states;

ALTER TABLE deck_pull_request_snapshots
    ADD COLUMN repository_hash bytea NOT NULL
        CHECK (octet_length(repository_hash) = 32),
    ADD COLUMN pull_request_number bigint NOT NULL
        CHECK (pull_request_number > 0),
    ADD CONSTRAINT deck_pull_request_snapshots_reference_unique
        UNIQUE (
            view_id, viewer_hash, repository_hash, pull_request_number
        );

CREATE TABLE deck_github_user_credentials (
    connection_id uuid NOT NULL
        REFERENCES deck_connections(connection_id) ON DELETE CASCADE,
    account_id uuid NOT NULL
        REFERENCES deck_accounts(account_id) ON DELETE CASCADE,
    github_user_id bigint NOT NULL CHECK (github_user_id > 0),
    wrapping_key_id text NOT NULL
        CHECK (wrapping_key_id ~ '^[A-Za-z0-9._-]{1,64}$'),
    user_access_token_ciphertext bytea NOT NULL
        CHECK (octet_length(user_access_token_ciphertext) > 0),
    user_refresh_token_ciphertext bytea,
    user_access_token_expires_at timestamptz,
    user_refresh_token_expires_at timestamptz,
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (connection_id, account_id),
    CHECK (substring(account_id::text, 15, 1) = '7')
);

CREATE TABLE deck_github_callback_states (
    state_hash bytea PRIMARY KEY CHECK (octet_length(state_hash) = 32),
    owner_hash bytea NOT NULL CHECK (octet_length(owner_hash) = 32),
    account_hash bytea NOT NULL CHECK (octet_length(account_hash) = 32),
    state_ciphertext bytea NOT NULL CHECK (octet_length(state_ciphertext) > 0),
    expires_at timestamptz NOT NULL,
    consumed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp()
);
CREATE INDEX deck_github_callback_states_expiry_idx
    ON deck_github_callback_states (expires_at);
CREATE INDEX deck_github_callback_states_owner_idx
    ON deck_github_callback_states (owner_hash);
CREATE INDEX deck_github_callback_states_account_idx
    ON deck_github_callback_states (account_hash);

CREATE TABLE deck_github_webhook_deliveries (
    delivery_id text PRIMARY KEY
        CHECK (delivery_id <> '' AND length(delivery_id) <= 128),
    event_type smallint NOT NULL CHECK (event_type IN (1, 2, 3, 4)),
    action_type smallint NOT NULL CHECK (action_type BETWEEN 1 AND 9),
    provider_identity_hash bytea NOT NULL
        CHECK (octet_length(provider_identity_hash) = 32),
    payload_hash bytea NOT NULL CHECK (octet_length(payload_hash) = 32),
    processed_at timestamptz NOT NULL
);

-- Nullable lifecycle rows provide stable transaction locks even before a
-- connection or credential exists. Provider identities remain keyed hashes.
CREATE TABLE deck_github_installation_states (
    provider_identity_hash bytea PRIMARY KEY
        CHECK (octet_length(provider_identity_hash) = 32),
    deleted_at timestamptz
);

CREATE TABLE deck_github_authorization_states (
    provider_identity_hash bytea PRIMARY KEY
        CHECK (octet_length(provider_identity_hash) = 32),
    revoked_at timestamptz,
    reauthorized_at timestamptz
);

-- Notification deliveries contain opaque identifiers only. They are included
-- here so disconnect can enforce one transaction-wide provider-data boundary.
CREATE TABLE deck_notification_events (
    event_id uuid PRIMARY KEY,
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    opaque_event_id bytea NOT NULL CHECK (octet_length(opaque_event_id) = 32),
    transition smallint NOT NULL CHECK (transition BETWEEN 1 AND 7),
    created_at timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    CHECK (substring(event_id::text, 15, 1) = '7'),
    UNIQUE (opaque_event_id)
);

ALTER TABLE deck_audit_events
    DROP CONSTRAINT deck_audit_events_event_type_check,
    ADD CONSTRAINT deck_audit_events_event_type_check
        CHECK (event_type BETWEEN 1 AND 16),
    DROP CONSTRAINT deck_audit_events_resource_type_check,
    ADD CONSTRAINT deck_audit_events_resource_type_check
        CHECK (resource_type BETWEEN 1 AND 6);
