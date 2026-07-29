-- name: InsertGitHubCallbackState :exec
INSERT INTO deck_github_callback_states (
    state_hash, owner_scope, owner_id, account_id, state_ciphertext, expires_at
) VALUES (
    sqlc.arg(state_hash), sqlc.arg(owner_scope), sqlc.arg(owner_id),
    sqlc.arg(account_id), sqlc.arg(state_ciphertext), sqlc.arg(expires_at)
);

-- name: GetGitHubCallbackStateForUpdate :one
SELECT *
FROM deck_github_callback_states
WHERE state_hash = sqlc.arg(state_hash)
FOR UPDATE;

-- name: DeleteGitHubCallbackState :exec
DELETE FROM deck_github_callback_states
WHERE state_hash = sqlc.arg(state_hash);

-- name: DeleteExpiredGitHubCallbackStates :exec
DELETE FROM deck_github_callback_states
WHERE expires_at <= sqlc.arg(expired_at);

-- name: DeleteGitHubCallbackStatesByOwner :exec
DELETE FROM deck_github_callback_states
WHERE owner_scope = sqlc.arg(owner_scope)
  AND owner_id = sqlc.arg(owner_id);

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

-- name: InsertGitHubConnection :one
INSERT INTO deck_connections (
    connection_id, owner_scope, owner_id, state,
    github_installation_id, github_account_id, github_account_kind,
    github_account_login_ciphertext, github_metadata_permission,
    github_pull_requests_permission, github_checks_permission,
    github_members_permission, revision, created_at, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(owner_scope), sqlc.arg(owner_id), 3,
    sqlc.arg(github_installation_id), sqlc.arg(github_account_id),
    sqlc.arg(github_account_kind), sqlc.arg(github_account_login_ciphertext),
    sqlc.arg(github_metadata_permission),
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
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE connection_id = sqlc.arg(connection_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: UpsertGitHubUserCredential :exec
INSERT INTO deck_github_user_credentials (
    connection_id, account_id, user_access_token_ciphertext,
    user_refresh_token_ciphertext, user_access_token_expires_at,
    user_refresh_token_expires_at, updated_at
) VALUES (
    sqlc.arg(connection_id), sqlc.arg(account_id),
    sqlc.arg(user_access_token_ciphertext),
    sqlc.narg(user_refresh_token_ciphertext),
    sqlc.narg(user_access_token_expires_at),
    sqlc.narg(user_refresh_token_expires_at), sqlc.arg(updated_at)
)
ON CONFLICT (connection_id, account_id) DO UPDATE
SET user_access_token_ciphertext = EXCLUDED.user_access_token_ciphertext,
    user_refresh_token_ciphertext = EXCLUDED.user_refresh_token_ciphertext,
    user_access_token_expires_at = EXCLUDED.user_access_token_expires_at,
    user_refresh_token_expires_at = EXCLUDED.user_refresh_token_expires_at,
    updated_at = EXCLUDED.updated_at;

-- name: GetGitHubUserCredential :one
SELECT *
FROM deck_github_user_credentials
WHERE connection_id = sqlc.arg(connection_id)
  AND account_id = sqlc.arg(account_id);

-- name: GetGitHubUserCredentialForAccount :one
SELECT credential.*
FROM deck_github_user_credentials credential
JOIN deck_connections connection
  ON connection.connection_id = credential.connection_id
WHERE credential.account_id = sqlc.arg(account_id)
  AND connection.state = 3
ORDER BY credential.updated_at DESC, credential.connection_id
LIMIT 1;

-- name: DeleteGitHubConnectionCredentials :exec
DELETE FROM deck_github_user_credentials
WHERE connection_id = sqlc.arg(connection_id);

-- name: DeleteGitHubUserCredentialsByAccount :exec
DELETE FROM deck_github_user_credentials
WHERE account_id = sqlc.arg(account_id);

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
WHERE (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
   OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id));

-- name: MarkOwnerViewsConnected :exec
UPDATE deck_views
SET connection_state = 3,
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE (sqlc.arg(owner_scope)::smallint = 1
        AND owner_scope = 1 AND owner_account_id = sqlc.arg(owner_id))
   OR (sqlc.arg(owner_scope)::smallint = 2
        AND owner_scope = 2 AND owner_organization_id = sqlc.arg(owner_id));

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
    delivery_id, event_type, action_type, installation_id,
    payload_hash, processed_at
) VALUES (
    sqlc.arg(delivery_id), sqlc.arg(event_type), sqlc.arg(action_type),
    sqlc.arg(installation_id), sqlc.arg(payload_hash), sqlc.arg(processed_at)
);
