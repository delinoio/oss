CREATE TABLE deck_accounts (
    account_id uuid PRIMARY KEY,
    logto_subject text NOT NULL UNIQUE CHECK (logto_subject <> ''),
    github_login_ciphertext bytea NOT NULL CHECK (octet_length(github_login_ciphertext) > 0),
    active boolean NOT NULL DEFAULT true,
    CHECK (substring(account_id::text, 15, 1) = '7')
);

CREATE TABLE deck_organization_memberships (
    organization_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES deck_accounts(account_id) ON DELETE CASCADE,
    role smallint NOT NULL CHECK (role IN (1, 2, 3)),
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (organization_id, account_id),
    CHECK (substring(organization_id::text, 15, 1) = '7')
);

CREATE TABLE deck_team_memberships (
    organization_id uuid NOT NULL,
    team_id uuid NOT NULL,
    account_id uuid NOT NULL REFERENCES deck_accounts(account_id) ON DELETE CASCADE,
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (organization_id, team_id, account_id),
    CHECK (substring(organization_id::text, 15, 1) = '7'),
    CHECK (substring(team_id::text, 15, 1) = '7')
);

CREATE TABLE deck_owner_locks (
    owner_hash bytea PRIMARY KEY CHECK (octet_length(owner_hash) = 32)
);

CREATE TABLE deck_views (
    view_id uuid PRIMARY KEY,
    owner_scope smallint NOT NULL CHECK (owner_scope IN (1, 2)),
    owner_account_id uuid,
    owner_organization_id uuid,
    billing_organization_id uuid,
    billing_team_id uuid,
    name_ciphertext bytea NOT NULL CHECK (octet_length(name_ciphertext) > 0),
    query_ciphertext bytea NOT NULL CHECK (octet_length(query_ciphertext) > 0),
    kind smallint NOT NULL CHECK (kind = 1),
    sort smallint NOT NULL CHECK (sort BETWEEN 1 AND 5),
    grouping smallint NOT NULL CHECK (grouping BETWEEN 1 AND 5),
    notification_ciphertext bytea NOT NULL CHECK (octet_length(notification_ciphertext) > 0),
    connection_state smallint NOT NULL DEFAULT 1 CHECK (connection_state BETWEEN 1 AND 4),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    snapshot_truncated boolean NOT NULL DEFAULT false,
    snapshot_refreshed_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (substring(view_id::text, 15, 1) = '7'),
    CHECK (
        (owner_scope = 1 AND owner_account_id IS NOT NULL AND owner_organization_id IS NULL)
        OR
        (owner_scope = 2 AND owner_account_id IS NULL AND owner_organization_id IS NOT NULL)
    ),
    CHECK (owner_account_id IS NULL OR substring(owner_account_id::text, 15, 1) = '7'),
    CHECK (owner_organization_id IS NULL OR substring(owner_organization_id::text, 15, 1) = '7'),
    CHECK (billing_organization_id IS NULL OR substring(billing_organization_id::text, 15, 1) = '7'),
    CHECK (billing_team_id IS NULL OR substring(billing_team_id::text, 15, 1) = '7')
);
CREATE INDEX deck_views_personal_owner_idx
    ON deck_views (owner_account_id, view_id)
    WHERE owner_scope = 1;
CREATE INDEX deck_views_organization_owner_idx
    ON deck_views (owner_organization_id, view_id)
    WHERE owner_scope = 2;

CREATE TABLE deck_view_create_idempotency (
    subject_hash bytea NOT NULL CHECK (octet_length(subject_hash) = 32),
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (subject_hash, idempotency_key),
    CHECK (substring(idempotency_key::text, 15, 1) = '7')
);

CREATE TABLE deck_pull_request_snapshots (
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    viewer_hash bytea NOT NULL CHECK (octet_length(viewer_hash) = 32),
    ordinal integer NOT NULL CHECK (ordinal >= 0 AND ordinal < 500),
    snapshot_ciphertext bytea NOT NULL CHECK (octet_length(snapshot_ciphertext) > 0),
    PRIMARY KEY (view_id, viewer_hash, ordinal)
);

CREATE TABLE deck_pull_request_snapshot_states (
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    viewer_hash bytea NOT NULL CHECK (octet_length(viewer_hash) = 32),
    truncated boolean NOT NULL DEFAULT false,
    refreshed_at timestamptz NOT NULL,
    PRIMARY KEY (view_id, viewer_hash)
);

CREATE TABLE deck_device_registrations (
    registration_id uuid PRIMARY KEY,
    device_id uuid NOT NULL UNIQUE,
    account_id uuid NOT NULL REFERENCES deck_accounts(account_id) ON DELETE CASCADE,
    platform smallint NOT NULL CHECK (platform BETWEEN 1 AND 5),
    display_name_ciphertext bytea NOT NULL CHECK (octet_length(display_name_ciphertext) > 0),
    push_ciphertext bytea NOT NULL CHECK (octet_length(push_ciphertext) > 0),
    detailed_notification_text_enabled boolean NOT NULL DEFAULT false,
    shortcuts_ciphertext bytea NOT NULL CHECK (octet_length(shortcuts_ciphertext) > 0),
    widgets_ciphertext bytea NOT NULL CHECK (octet_length(widgets_ciphertext) > 0),
    grant_verifier bytea NOT NULL CHECK (octet_length(grant_verifier) = 32),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    lease_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (substring(registration_id::text, 15, 1) = '7'),
    CHECK (substring(device_id::text, 15, 1) = '7'),
    CHECK (substring(account_id::text, 15, 1) = '7')
);
CREATE INDEX deck_device_registrations_account_idx
    ON deck_device_registrations (account_id, device_id);
CREATE INDEX deck_device_registrations_grant_idx
    ON deck_device_registrations (grant_verifier);

CREATE TABLE deck_device_registration_idempotency (
    account_id uuid NOT NULL,
    idempotency_key uuid NOT NULL,
    request_digest bytea NOT NULL CHECK (octet_length(request_digest) = 32),
    registration_id uuid NOT NULL REFERENCES deck_device_registrations(registration_id) ON DELETE CASCADE,
    grant_replay_ciphertext bytea NOT NULL CHECK (octet_length(grant_replay_ciphertext) > 0),
    lease_expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (account_id, idempotency_key),
    CHECK (substring(account_id::text, 15, 1) = '7'),
    CHECK (substring(idempotency_key::text, 15, 1) = '7')
);

CREATE TABLE deck_view_notification_preferences (
    registration_id uuid NOT NULL REFERENCES deck_device_registrations(registration_id) ON DELETE CASCADE,
    view_id uuid NOT NULL REFERENCES deck_views(view_id) ON DELETE CASCADE,
    preference_ciphertext bytea NOT NULL CHECK (octet_length(preference_ciphertext) > 0),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (registration_id, view_id)
);

CREATE TABLE deck_connections (
    connection_id uuid PRIMARY KEY,
    owner_scope smallint NOT NULL CHECK (owner_scope IN (1, 2)),
    owner_id uuid NOT NULL,
    state smallint NOT NULL CHECK (state BETWEEN 1 AND 4),
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (owner_scope, owner_id),
    CHECK (substring(connection_id::text, 15, 1) = '7'),
    CHECK (substring(owner_id::text, 15, 1) = '7')
);

CREATE TABLE deck_owner_tombstones (
    target_hash bytea PRIMARY KEY CHECK (octet_length(target_hash) = 32),
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp()
);

CREATE TABLE deck_deletion_jobs (
    deletion_job_id uuid PRIMARY KEY,
    replay_key uuid NOT NULL UNIQUE,
    target_hash bytea NOT NULL CHECK (octet_length(target_hash) = 32),
    trigger smallint NOT NULL CHECK (trigger IN (1, 2, 3)),
    state smallint NOT NULL CHECK (state IN (1, 2, 3)),
    accepted_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    completed_at timestamptz,
    CHECK (substring(deletion_job_id::text, 15, 1) = '7'),
    CHECK (substring(replay_key::text, 15, 1) = '7')
);

CREATE TABLE deck_audit_events (
    audit_id uuid PRIMARY KEY,
    event_type smallint NOT NULL CHECK (event_type BETWEEN 1 AND 12),
    actor_pseudonym text NOT NULL CHECK (actor_pseudonym ~ '^actor:v1:[0-9a-f]{32}$'),
    owner_scope smallint CHECK (owner_scope IN (1, 2)),
    target_hash bytea CHECK (target_hash IS NULL OR octet_length(target_hash) = 32),
    resource_type smallint NOT NULL CHECK (resource_type BETWEEN 1 AND 5),
    resource_id uuid,
    outcome smallint NOT NULL CHECK (outcome IN (1, 2, 3)),
    occurred_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (substring(audit_id::text, 15, 1) = '7'),
    CHECK (resource_id IS NULL OR substring(resource_id::text, 15, 1) = '7')
);
CREATE INDEX deck_audit_events_occurred_idx ON deck_audit_events (occurred_at, audit_id);
