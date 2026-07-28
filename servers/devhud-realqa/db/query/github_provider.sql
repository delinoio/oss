-- name: ConsumeGitHubCallbackState :execrows
INSERT INTO realqa_github_callback_states (nonce)
VALUES (sqlc.arg(nonce))
ON CONFLICT (nonce) DO NOTHING;

-- name: ConnectGitHubUser :execrows
UPDATE realqa_github_connections
SET state = 'connected',
    github_login = sqlc.arg(github_login),
    github_user_id = sqlc.arg(github_user_id),
    credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    connected_at = transaction_timestamp(),
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: CreatePendingGitHubInstallation :execrows
INSERT INTO realqa_github_installations (
    id, connection_id, owner_kind, owner_id, provider_installation_id,
    account_login, state
)
SELECT
    sqlc.arg(id), connection.id, connection.owner_kind, connection.owner_id,
    sqlc.arg(provider_installation_id), 'pending', 'pending'
FROM realqa_github_connections AS connection
WHERE connection.owner_kind = sqlc.arg(owner_kind)
  AND connection.owner_id = sqlc.arg(owner_id)
ON CONFLICT (provider_installation_id) DO NOTHING;

-- name: GetGitHubInstallationBinding :one
SELECT owner_kind, owner_id
FROM realqa_github_installations
WHERE provider_installation_id = sqlc.arg(provider_installation_id);

-- name: GetGitHubUserCredentialForInstallation :one
SELECT
    installation.provider_installation_id,
    installation.owner_kind,
    installation.owner_id,
    connection.credential_ciphertext,
    connection.wrapped_data_key,
    connection.key_id
FROM realqa_github_installations AS installation
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE installation.id = sqlc.arg(installation_id)
  AND installation.state = 'active';

-- name: ActivateGitHubInstallation :execrows
UPDATE realqa_github_installations
SET provider_account_id = sqlc.arg(provider_account_id),
    account_login = sqlc.arg(account_login),
    account_kind = sqlc.arg(account_kind),
    state = 'active',
    permissions = sqlc.arg(permissions),
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE provider_installation_id = sqlc.arg(provider_installation_id)
  AND state <> 'deleted';

-- name: SetGitHubInstallationState :execrows
UPDATE realqa_github_installations
SET state = sqlc.arg(state),
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE provider_installation_id = sqlc.arg(provider_installation_id);

-- name: RecordGitHubWebhookDelivery :execrows
INSERT INTO realqa_github_webhook_deliveries (delivery_id)
VALUES (sqlc.arg(delivery_id))
ON CONFLICT (delivery_id) DO NOTHING;

-- name: RemoveGitHubRepositoryAccess :execrows
DELETE FROM realqa_repository_access
WHERE installation_id = (
    SELECT id
    FROM realqa_github_installations
    WHERE provider_installation_id = sqlc.arg(provider_installation_id)
)
AND repository_id = sqlc.arg(repository_id);

-- name: RemoveGitHubRepositoryDefinitions :execrows
DELETE FROM realqa_repository_definitions
WHERE installation_id = (
    SELECT id
    FROM realqa_github_installations
    WHERE provider_installation_id = sqlc.arg(provider_installation_id)
)
AND repository_id = sqlc.arg(repository_id);

-- name: MarkAssetsRemovedForDeletedGitHubIssue :execrows
UPDATE realqa_assets AS asset
SET state = 'removed_placeholder',
    object_key_ciphertext = NULL,
    removed_at = COALESCE(asset.removed_at, transaction_timestamp()),
    revision = asset.revision + 1
FROM realqa_submissions AS submission
JOIN realqa_destinations AS destination
  ON destination.id = submission.destination_id
JOIN realqa_github_installations AS installation
  ON installation.id = destination.installation_id
WHERE asset.submission_id = submission.id
  AND installation.provider_installation_id = sqlc.arg(provider_installation_id)
  AND destination.repository_id = sqlc.arg(repository_id)
  AND submission.provider_issue_id = sqlc.arg(provider_issue_id)
  AND asset.state IN ('private_staging', 'verified_unlinked', 'public_retained');

-- name: DisconnectGitHubUserCredentials :execrows
UPDATE realqa_github_connections
SET state = 'disconnected',
    credential_ciphertext = NULL,
    wrapped_data_key = NULL,
    key_id = NULL,
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE github_user_id = sqlc.arg(github_user_id)
  AND credential_ciphertext IS NOT NULL;
