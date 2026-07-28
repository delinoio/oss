CREATE TABLE realqa_github_connections (
    id uuid PRIMARY KEY,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('disconnected', 'pending', 'connected')),
    github_login text NOT NULL DEFAULT '',
    credential_ciphertext bytea,
    wrapped_data_key bytea,
    key_id text,
    oauth_state_digest bytea,
    oauth_state_expires_at timestamptz,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    connected_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (owner_kind, owner_id),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (
        (credential_ciphertext IS NULL AND wrapped_data_key IS NULL AND key_id IS NULL)
        OR
        (
            credential_ciphertext IS NOT NULL
            AND octet_length(credential_ciphertext) > 0
            AND wrapped_data_key IS NOT NULL
            AND octet_length(wrapped_data_key) > 0
            AND length(key_id) BETWEEN 1 AND 128
        )
    ),
    CHECK ((state = 'connected') = (credential_ciphertext IS NOT NULL)),
    CHECK (
        (oauth_state_digest IS NULL AND oauth_state_expires_at IS NULL)
        OR
        (
            state IN ('pending', 'connected')
            AND octet_length(oauth_state_digest) = 32
            AND oauth_state_expires_at > created_at
        )
    )
);

CREATE TABLE realqa_github_installations (
    id uuid PRIMARY KEY,
    connection_id uuid NOT NULL REFERENCES realqa_github_connections(id) ON DELETE CASCADE,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    provider_installation_id bigint NOT NULL CHECK (provider_installation_id > 0),
    account_login text NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (provider_installation_id),
    UNIQUE (owner_kind, owner_id, id),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (length(account_login) BETWEEN 1 AND 255)
);

CREATE TABLE realqa_repository_access (
    installation_id uuid NOT NULL REFERENCES realqa_github_installations(id) ON DELETE CASCADE,
    account_id uuid NOT NULL REFERENCES realqa_identities(account_id) ON DELETE CASCADE,
    repository_id text NOT NULL,
    repository_owner text NOT NULL,
    repository_name text NOT NULL,
    issues_enabled boolean NOT NULL,
    can_submit boolean NOT NULL,
    checked_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (installation_id, account_id, repository_id),
    CHECK (length(repository_id) BETWEEN 1 AND 255),
    CHECK (length(repository_owner) BETWEEN 1 AND 255),
    CHECK (length(repository_name) BETWEEN 1 AND 255)
);

CREATE TABLE realqa_repository_definitions (
    installation_id uuid NOT NULL REFERENCES realqa_github_installations(id) ON DELETE CASCADE,
    repository_id text NOT NULL,
    kind text NOT NULL CHECK (kind IN ('markdown_template', 'issue_form')),
    definition_id text NOT NULL,
    name text NOT NULL,
    path text NOT NULL,
    etag text NOT NULL,
    schema_payload jsonb NOT NULL,
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    PRIMARY KEY (installation_id, repository_id, kind, definition_id),
    CHECK (jsonb_typeof(schema_payload) = 'object'),
    CHECK (pg_column_size(schema_payload) <= 262144),
    CHECK (length(definition_id) BETWEEN 1 AND 255),
    CHECK (length(path) BETWEEN 1 AND 1024),
    CHECK (length(etag) BETWEEN 1 AND 255)
);

CREATE TABLE realqa_destinations (
    id uuid PRIMARY KEY,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    installation_id uuid NOT NULL REFERENCES realqa_github_installations(id),
    repository_id text NOT NULL,
    repository_owner text NOT NULL,
    repository_name text NOT NULL,
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (owner_kind, owner_id, installation_id, repository_id),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (length(repository_id) BETWEEN 1 AND 255),
    CHECK (length(repository_owner) BETWEEN 1 AND 255),
    CHECK (length(repository_name) BETWEEN 1 AND 255)
);

CREATE TABLE realqa_presets (
    id uuid PRIMARY KEY,
    owner_kind text NOT NULL CHECK (owner_kind IN ('personal', 'organization')),
    owner_id uuid NOT NULL,
    created_by_account_id uuid NOT NULL REFERENCES realqa_identities(account_id),
    payer_organization_id uuid NOT NULL,
    payer_team_id uuid NOT NULL,
    destination_id uuid NOT NULL REFERENCES realqa_destinations(id),
    name text NOT NULL,
    capture_mode text NOT NULL CHECK (capture_mode IN (
        'region', 'window', 'full_display', 'multi_monitor', 'chrome_visible_viewport'
    )),
    include_pointer boolean NOT NULL,
    selector_mode text NOT NULL CHECK (selector_mode IN ('normal', 'dom')),
    issue_definition_kind text NOT NULL CHECK (issue_definition_kind IN (
        'markdown_template', 'issue_form'
    )),
    issue_definition_id text NOT NULL,
    issue_definition_name text NOT NULL,
    issue_definition_path text NOT NULL,
    issue_definition_etag text NOT NULL,
    default_labels text[] NOT NULL DEFAULT '{}',
    default_assignees text[] NOT NULL DEFAULT '{}',
    milestone_number bigint,
    project_node_ids text[] NOT NULL DEFAULT '{}',
    revision bigint NOT NULL DEFAULT 1 CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    updated_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    UNIQUE (owner_kind, owner_id, id),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (realqa_is_uuid_v7(owner_id)),
    CHECK (realqa_is_uuid_v7(payer_organization_id)),
    CHECK (realqa_is_uuid_v7(payer_team_id)),
    CHECK (length(name) BETWEEN 1 AND 120),
    CHECK (cardinality(default_labels) <= 100),
    CHECK (cardinality(default_assignees) <= 100),
    CHECK (cardinality(project_node_ids) <= 20),
    CHECK (milestone_number IS NULL OR milestone_number > 0)
);

CREATE INDEX realqa_presets_owner_idx
    ON realqa_presets(owner_kind, owner_id, id);

CREATE TABLE realqa_process_url_rules (
    id uuid PRIMARY KEY,
    preset_id uuid NOT NULL REFERENCES realqa_presets(id) ON DELETE CASCADE,
    ordinal integer NOT NULL CHECK (ordinal >= 0 AND ordinal < 100),
    exact_process_name text NOT NULL,
    safe_window_title_pattern text NOT NULL DEFAULT '',
    url_template text NOT NULL,
    enabled boolean NOT NULL,
    UNIQUE (preset_id, ordinal),
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (length(exact_process_name) BETWEEN 1 AND 255),
    CHECK (length(safe_window_title_pattern) <= 512),
    CHECK (length(url_template) BETWEEN 1 AND 2048)
);

CREATE TABLE realqa_shortcuts (
    id uuid PRIMARY KEY,
    preset_id uuid NOT NULL UNIQUE REFERENCES realqa_presets(id) ON DELETE CASCADE,
    accelerator text NOT NULL,
    active boolean NOT NULL,
    CHECK (realqa_is_uuid_v7(id)),
    CHECK (length(accelerator) BETWEEN 1 AND 128)
);
