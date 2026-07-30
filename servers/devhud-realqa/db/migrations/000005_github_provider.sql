ALTER TABLE realqa_github_connections
    ADD COLUMN github_user_id bigint,
    ADD COLUMN connected_by_account_id uuid
        REFERENCES realqa_identities(account_id) ON DELETE SET NULL;

UPDATE realqa_github_connections
SET state = 'disconnected',
    connected_by_account_id = NULL,
    credential_ciphertext = NULL,
    wrapped_data_key = NULL,
    key_id = NULL,
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE state = 'connected'
  AND github_user_id IS NULL;

ALTER TABLE realqa_github_connections
    ADD CONSTRAINT realqa_github_connections_user_id_valid
    CHECK (github_user_id IS NULL OR github_user_id > 0);

ALTER TABLE realqa_github_installations
    ADD COLUMN provider_account_id bigint,
    ADD COLUMN account_kind text,
    ADD COLUMN state text NOT NULL DEFAULT 'pending',
    ADD COLUMN permissions jsonb NOT NULL DEFAULT '{}'::jsonb;

UPDATE realqa_github_installations
SET provider_account_id = provider_installation_id,
    account_kind = CASE owner_kind
        WHEN 'personal' THEN 'User'
        ELSE 'Organization'
    END,
    state = 'active',
    permissions = jsonb_build_object(
        'issues', 'write',
        'metadata', 'read',
        'contents', 'read'
    );

ALTER TABLE realqa_github_installations
    ADD CONSTRAINT realqa_github_installations_account_id_valid
    CHECK (provider_account_id IS NULL OR provider_account_id > 0),
    ADD CONSTRAINT realqa_github_installations_account_kind_valid
    CHECK (account_kind IS NULL OR account_kind IN ('User', 'Organization')),
    ADD CONSTRAINT realqa_github_installations_state_valid
    CHECK (state IN ('pending', 'active', 'suspended', 'deleted')),
    ADD CONSTRAINT realqa_github_installations_active_complete
    CHECK (
        state <> 'active'
        OR (
            provider_account_id IS NOT NULL
            AND account_kind IS NOT NULL
            AND permissions @> '{"issues":"write","metadata":"read","contents":"read"}'::jsonb
        )
    ),
    ADD CONSTRAINT realqa_github_installations_permissions_object
    CHECK (
        jsonb_typeof(permissions) = 'object'
        AND pg_column_size(permissions) <= 4096
    );

CREATE TABLE realqa_github_callback_states (
    nonce text PRIMARY KEY,
    consumed_at timestamptz NOT NULL DEFAULT transaction_timestamp(),
    CHECK (length(nonce) = 32)
);

CREATE TABLE realqa_github_webhook_deliveries (
    delivery_id uuid PRIMARY KEY,
    received_at timestamptz NOT NULL DEFAULT transaction_timestamp()
);

CREATE INDEX realqa_github_connections_user_idx
    ON realqa_github_connections(github_user_id)
    WHERE github_user_id IS NOT NULL;

CREATE INDEX realqa_github_connections_account_idx
    ON realqa_github_connections(connected_by_account_id)
    WHERE connected_by_account_id IS NOT NULL;

CREATE INDEX realqa_github_installations_provider_state_idx
    ON realqa_github_installations(provider_installation_id, state);
