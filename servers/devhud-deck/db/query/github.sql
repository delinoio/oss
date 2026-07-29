-- name: InsertGitHubCallbackState :exec
INSERT INTO deck_github_callback_states (
    state_hash, owner_hash, account_hash, state_ciphertext, expires_at,
    created_at
) VALUES (
    sqlc.arg(state_hash), sqlc.arg(owner_hash), sqlc.arg(account_hash),
    sqlc.arg(state_ciphertext), sqlc.arg(expires_at), sqlc.arg(created_at)
);

-- name: GetGitHubCallbackStateForUpdate :one
SELECT *
FROM deck_github_callback_states
WHERE state_hash = sqlc.arg(state_hash)
FOR UPDATE;

-- name: MarkGitHubCallbackStateConsumed :exec
UPDATE deck_github_callback_states
SET consumed_at = sqlc.arg(consumed_at)
WHERE state_hash = sqlc.arg(state_hash)
  AND consumed_at IS NULL;

-- name: DeleteGitHubCallbackState :exec
DELETE FROM deck_github_callback_states
WHERE state_hash = sqlc.arg(state_hash);

-- name: DeleteConsumedGitHubCallbackState :one
DELETE FROM deck_github_callback_states
WHERE state_hash = sqlc.arg(state_hash)
  AND owner_hash = sqlc.arg(owner_hash)
  AND account_hash = sqlc.arg(account_hash)
  AND consumed_at IS NOT NULL
RETURNING created_at;

-- name: DeleteExpiredGitHubCallbackStates :exec
DELETE FROM deck_github_callback_states
WHERE expires_at <= sqlc.arg(expired_at);

-- name: DeleteGitHubCallbackStatesByOwner :exec
DELETE FROM deck_github_callback_states
WHERE owner_hash = sqlc.arg(owner_hash);

-- name: DeleteGitHubCallbackStatesByAccount :exec
DELETE FROM deck_github_callback_states
WHERE account_hash = sqlc.arg(account_hash);

-- name: GetGitHubConnectionByOwner :one
SELECT *
FROM deck_connections
WHERE owner_scope = sqlc.arg(owner_scope)
  AND owner_id = sqlc.arg(owner_id);

-- name: GetGitHubConnectionByOwnerForUpdate :one
SELECT *
FROM deck_connections
WHERE owner_scope = sqlc.arg(owner_scope)
  AND owner_id = sqlc.arg(owner_id)
FOR UPDATE;

-- name: GetGitHubConnectionByIDForUpdate :one
SELECT *
FROM deck_connections
WHERE connection_id = sqlc.arg(connection_id)
FOR UPDATE;

-- name: GetGitHubConnectionOwnerByID :one
SELECT owner_scope, owner_id
FROM deck_connections
WHERE connection_id = sqlc.arg(connection_id);

-- name: GetGitHubConnectionByInstallationForUpdate :one
SELECT *
FROM deck_connections
WHERE github_installation_id = sqlc.arg(github_installation_id)
FOR UPDATE;

-- name: CanManageOrganizationForGitHubCallback :one
SELECT EXISTS (
    SELECT 1
    FROM deck_organization_memberships
    WHERE organization_id = sqlc.arg(organization_id)
      AND account_id = sqlc.arg(account_id)
      AND active
      AND role >= 2
)::boolean;

-- name: CanUseOrganizationForGitHubCallback :one
SELECT EXISTS (
    SELECT 1
    FROM deck_organization_memberships
    WHERE organization_id = sqlc.arg(organization_id)
      AND account_id = sqlc.arg(account_id)
      AND active
)::boolean;

-- name: InsertGitHubConnection :one
INSERT INTO deck_connections (
    connection_id, owner_scope, owner_id, state,
    github_installation_id, github_account_id, github_account_kind,
    github_account_login_ciphertext, github_metadata_permission,
    github_administration_permission, github_contents_permission,
    github_pull_requests_permission, github_checks_permission,
    github_members_permission, revision, created_at, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(owner_scope), sqlc.arg(owner_id), 3,
    sqlc.arg(github_installation_id), sqlc.arg(github_account_id),
    sqlc.arg(github_account_kind), sqlc.arg(github_account_login_ciphertext),
    sqlc.arg(github_metadata_permission),
    sqlc.arg(github_administration_permission),
    sqlc.arg(github_contents_permission),
    sqlc.arg(github_pull_requests_permission),
    sqlc.arg(github_checks_permission), sqlc.arg(github_members_permission), 1,
    sqlc.arg(created_at), sqlc.arg(updated_at)
)
RETURNING *;

-- name: ReconnectGitHubConnection :one
UPDATE deck_connections
SET state = 3,
    github_installation_id = sqlc.arg(github_installation_id),
    github_account_id = sqlc.arg(github_account_id),
    github_account_kind = sqlc.arg(github_account_kind),
    github_account_login_ciphertext = sqlc.arg(github_account_login_ciphertext),
    github_metadata_permission = sqlc.arg(github_metadata_permission),
    github_administration_permission =
        sqlc.arg(github_administration_permission),
    github_contents_permission = sqlc.arg(github_contents_permission),
    github_pull_requests_permission = sqlc.arg(github_pull_requests_permission),
    github_checks_permission = sqlc.arg(github_checks_permission),
    github_members_permission = sqlc.arg(github_members_permission),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE connection_id = sqlc.arg(connection_id)
RETURNING *;

-- name: DisconnectGitHubConnection :one
UPDATE deck_connections
SET state = sqlc.arg(connection_state),
    github_installation_id = NULL,
    github_account_id = NULL,
    github_account_kind = NULL,
    github_account_login_ciphertext = NULL,
    github_metadata_permission = NULL,
    github_administration_permission = NULL,
    github_contents_permission = NULL,
    github_pull_requests_permission = NULL,
    github_checks_permission = NULL,
    github_members_permission = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE connection_id = sqlc.arg(connection_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: RequireGitHubReauthentication :one
UPDATE deck_connections
SET state = 4,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE connection_id = sqlc.arg(connection_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: UpdateGitHubInstallationPermissions :one
UPDATE deck_connections
SET github_metadata_permission = sqlc.arg(github_metadata_permission),
    github_administration_permission =
        sqlc.arg(github_administration_permission),
    github_contents_permission = sqlc.arg(github_contents_permission),
    github_pull_requests_permission = sqlc.arg(github_pull_requests_permission),
    github_checks_permission = sqlc.arg(github_checks_permission),
    github_members_permission = sqlc.arg(github_members_permission),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE connection_id = sqlc.arg(connection_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: UpdateGitHubUserCredentials :exec
UPDATE deck_github_user_credentials
SET wrapping_key_id = sqlc.arg(wrapping_key_id),
    user_access_token_ciphertext = sqlc.arg(user_access_token_ciphertext),
    user_refresh_token_ciphertext = sqlc.narg(user_refresh_token_ciphertext),
    user_access_token_expires_at = sqlc.narg(user_access_token_expires_at),
    user_refresh_token_expires_at = sqlc.narg(user_refresh_token_expires_at),
    updated_at = sqlc.arg(updated_at)
WHERE account_id = sqlc.arg(account_id)
  AND github_user_id = sqlc.arg(github_user_id);

-- name: UpsertGitHubUserCredential :exec
INSERT INTO deck_github_user_credentials (
    connection_id, account_id, github_user_id, wrapping_key_id,
    user_access_token_ciphertext, user_refresh_token_ciphertext,
    user_access_token_expires_at,
    user_refresh_token_expires_at, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(account_id), sqlc.arg(github_user_id),
    sqlc.arg(wrapping_key_id),
    sqlc.arg(user_access_token_ciphertext),
    sqlc.narg(user_refresh_token_ciphertext),
    sqlc.narg(user_access_token_expires_at),
    sqlc.narg(user_refresh_token_expires_at), sqlc.arg(updated_at)
)
ON CONFLICT (connection_id, account_id) DO UPDATE
SET github_user_id = EXCLUDED.github_user_id,
    wrapping_key_id = EXCLUDED.wrapping_key_id,
    user_access_token_ciphertext = EXCLUDED.user_access_token_ciphertext,
    user_refresh_token_ciphertext = EXCLUDED.user_refresh_token_ciphertext,
    user_access_token_expires_at = EXCLUDED.user_access_token_expires_at,
    user_refresh_token_expires_at = EXCLUDED.user_refresh_token_expires_at,
    updated_at = EXCLUDED.updated_at;

-- name: GetGitHubUserCredential :one
SELECT *
FROM deck_github_user_credentials
WHERE connection_id = sqlc.arg(connection_id)
  AND account_id = sqlc.arg(account_id);

-- name: ListGitHubUserCredentialsForRewrap :many
SELECT *
FROM deck_github_user_credentials
ORDER BY connection_id, account_id
FOR UPDATE;

-- name: RewrapGitHubUserCredential :exec
UPDATE deck_github_user_credentials
SET wrapping_key_id = sqlc.arg(wrapping_key_id),
    user_access_token_ciphertext = sqlc.arg(user_access_token_ciphertext),
    user_refresh_token_ciphertext = sqlc.narg(user_refresh_token_ciphertext)
WHERE connection_id = sqlc.arg(connection_id)
  AND account_id = sqlc.arg(account_id);

-- name: DeleteGitHubConnectionCredentials :exec
DELETE FROM deck_github_user_credentials
WHERE connection_id = sqlc.arg(connection_id);

-- name: DeleteGitHubUserCredentialsByAccount :exec
DELETE FROM deck_github_user_credentials
WHERE account_id = sqlc.arg(account_id);

-- name: DeleteExpiredGitHubUserCredentialsByAccountAndGitHubUser :exec
DELETE FROM deck_github_user_credentials
WHERE account_id = sqlc.arg(account_id)
  AND github_user_id = sqlc.arg(github_user_id)
  AND user_access_token_expires_at <= sqlc.arg(expired_at);

-- name: DeleteGitHubUserCredentialsByGitHubUser :exec
DELETE FROM deck_github_user_credentials
WHERE github_user_id = sqlc.arg(github_user_id);

-- name: EnsureGitHubInstallationState :exec
INSERT INTO deck_github_installation_states (provider_identity_hash)
VALUES (sqlc.arg(provider_identity_hash))
ON CONFLICT (provider_identity_hash) DO NOTHING;

-- name: LockGitHubInstallationState :one
SELECT deleted_at
FROM deck_github_installation_states
WHERE provider_identity_hash = sqlc.arg(provider_identity_hash)
FOR UPDATE;

-- name: MarkGitHubInstallationDeleted :exec
UPDATE deck_github_installation_states
SET deleted_at = CASE
    WHEN deleted_at IS NULL OR deleted_at < sqlc.arg(deleted_at)
        THEN sqlc.arg(deleted_at)
    ELSE deleted_at
END
WHERE provider_identity_hash = sqlc.arg(provider_identity_hash);

-- name: EnsureGitHubAuthorizationState :exec
INSERT INTO deck_github_authorization_states (provider_identity_hash)
VALUES (sqlc.arg(provider_identity_hash))
ON CONFLICT (provider_identity_hash) DO NOTHING;

-- name: LockGitHubAuthorizationState :one
SELECT revoked_at, reauthorized_at
FROM deck_github_authorization_states
WHERE provider_identity_hash = sqlc.arg(provider_identity_hash)
FOR UPDATE;

-- name: MarkGitHubAuthorizationRevoked :exec
UPDATE deck_github_authorization_states
SET revoked_at = CASE
    WHEN revoked_at IS NULL OR revoked_at < sqlc.arg(revoked_at)
        THEN sqlc.arg(revoked_at)
    ELSE revoked_at
END
WHERE provider_identity_hash = sqlc.arg(provider_identity_hash);

-- name: MarkGitHubAuthorizationReauthorized :exec
UPDATE deck_github_authorization_states
SET reauthorized_at = CASE
    WHEN reauthorized_at IS NULL OR reauthorized_at < sqlc.arg(reauthorized_at)
        THEN sqlc.arg(reauthorized_at)
    ELSE reauthorized_at
END
WHERE provider_identity_hash = sqlc.arg(provider_identity_hash);

-- name: ListOwnerViewsForProviderCleanup :many
SELECT view_id
FROM deck_views
WHERE (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
   OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
ORDER BY view_id
FOR UPDATE;

-- name: MarkOwnerViewsDisconnected :exec
UPDATE deck_views
SET connection_state = 1,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE (
    (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
    OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
)
  AND connection_state <> 1;

-- name: MarkOwnerViewsConnected :exec
UPDATE deck_views
SET connection_state = 3,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE (
    (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
    OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
)
  AND connection_state <> 3
  AND repository_authorization_index IS NOT NULL;

-- name: InvalidateOwnerViewsForProviderRename :exec
UPDATE deck_views
SET connection_state = 1,
    repository_authorization_index = NULL,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE (
    (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
    OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
)
  AND (connection_state <> 1 OR repository_authorization_index IS NOT NULL);

-- name: DeleteOwnerNotificationState :exec
DELETE FROM deck_view_notification_preferences
WHERE view_id IN (
    SELECT view_id FROM deck_views
    WHERE (sqlc.arg(owner_scope)::smallint = 1
            AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
       OR (sqlc.arg(owner_scope)::smallint = 2
            AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
);

-- name: DeleteOwnerNotificationEvents :exec
DELETE FROM deck_notification_events
WHERE view_id IN (
    SELECT view_id FROM deck_views
    WHERE (sqlc.arg(owner_scope)::smallint = 1
            AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
       OR (sqlc.arg(owner_scope)::smallint = 2
            AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id))
);

-- name: GetGitHubWebhookDelivery :one
SELECT *
FROM deck_github_webhook_deliveries
WHERE delivery_id = sqlc.arg(delivery_id);

-- name: InsertGitHubWebhookDelivery :exec
INSERT INTO deck_github_webhook_deliveries (
    delivery_id, event_type, action_type, provider_identity_hash, payload_hash,
    processed_at
) VALUES (
    sqlc.arg(delivery_id), sqlc.arg(event_type), sqlc.arg(action_type),
    sqlc.arg(provider_identity_hash), sqlc.arg(payload_hash),
    sqlc.arg(processed_at)
);
