CREATE TABLE realqa_github_user_authorizations (
    connection_id uuid NOT NULL
        REFERENCES realqa_github_connections(id) ON DELETE CASCADE,
    account_id uuid NOT NULL
        REFERENCES realqa_identities(account_id) ON DELETE CASCADE,
    state text NOT NULL CHECK (state IN ('disconnected', 'pending', 'connected')),
    github_user_id bigint,
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
    PRIMARY KEY (connection_id, account_id),
    CHECK (github_user_id IS NULL OR github_user_id > 0),
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
    CHECK (
        (state = 'connected') = (
            credential_ciphertext IS NOT NULL
            AND github_user_id IS NOT NULL
            AND length(github_login) BETWEEN 1 AND 255
            AND connected_at IS NOT NULL
        )
    ),
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

CREATE INDEX realqa_github_user_authorizations_user_idx
    ON realqa_github_user_authorizations(github_user_id)
    WHERE github_user_id IS NOT NULL;

ALTER TABLE realqa_audits
    DROP CONSTRAINT realqa_audits_event_type_check,
    ADD CONSTRAINT realqa_audits_event_type_check CHECK (event_type IN (
        'preset_created',
        'preset_updated',
        'preset_deleted',
        'github_connection_started',
        'github_user_authorization_started',
        'github_connection_disconnected',
        'feature_deletion_accepted',
        'repository_access_denied',
        'submission_created',
        'image_upload_authorized',
        'image_upload_verified',
        'image_deleted',
        'submission_assets_deleted'
    ));
