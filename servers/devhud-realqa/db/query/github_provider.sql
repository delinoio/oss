-- name: ConsumeGitHubCallbackState :execrows
INSERT INTO realqa_github_callback_states (nonce)
VALUES (sqlc.arg(nonce))
ON CONFLICT (nonce) DO NOTHING;

-- name: AdvanceGitHubCallbackState :execrows
UPDATE realqa_github_connections
SET oauth_state_digest = sqlc.arg(oauth_state_digest),
    oauth_state_expires_at = sqlc.arg(oauth_state_expires_at),
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id)
  AND oauth_state_digest = sqlc.arg(previous_oauth_state_digest)
  AND oauth_state_expires_at > transaction_timestamp();

-- name: ConnectGitHubUser :execrows
UPDATE realqa_github_connections
SET state = 'connected',
    github_login = sqlc.arg(github_login),
    github_user_id = sqlc.arg(github_user_id),
    connected_by_account_id = sqlc.arg(connected_by_account_id),
    credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    connected_at = transaction_timestamp(),
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id)
  AND oauth_state_digest = sqlc.arg(oauth_state_digest)
  AND oauth_state_expires_at > transaction_timestamp();

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
    connection.id AS connection_id,
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
  AND connection.connected_by_account_id = sqlc.arg(account_id)
  AND installation.state = 'active';

-- name: GetGitHubCallerAuthorizationForInstallation :one
SELECT
    caller_authorization.connection_id,
    installation.provider_installation_id,
    installation.owner_kind,
    installation.owner_id,
    caller_authorization.credential_ciphertext,
    caller_authorization.wrapped_data_key,
    caller_authorization.key_id
FROM realqa_github_user_authorizations AS caller_authorization
JOIN realqa_github_connections AS connection
  ON connection.id = caller_authorization.connection_id
 AND connection.state = 'connected'
JOIN realqa_github_installations AS installation
  ON installation.connection_id = connection.id
 AND installation.state = 'active'
WHERE installation.id = sqlc.arg(installation_id)
  AND caller_authorization.account_id = sqlc.arg(account_id)
  AND caller_authorization.state = 'connected';

-- name: GetGitHubUserCredentialForInstallationForUpdate :one
SELECT
    connection.id AS connection_id,
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
  AND connection.connected_by_account_id = sqlc.arg(account_id)
  AND installation.state = 'active'
FOR UPDATE OF connection;

-- name: GetGitHubCallerAuthorizationForInstallationForUpdate :one
SELECT
    caller_authorization.connection_id,
    installation.provider_installation_id,
    installation.owner_kind,
    installation.owner_id,
    caller_authorization.credential_ciphertext,
    caller_authorization.wrapped_data_key,
    caller_authorization.key_id
FROM realqa_github_user_authorizations AS caller_authorization
JOIN realqa_github_connections AS connection
  ON connection.id = caller_authorization.connection_id
 AND connection.state = 'connected'
JOIN realqa_github_installations AS installation
  ON installation.connection_id = connection.id
 AND installation.state = 'active'
WHERE installation.id = sqlc.arg(installation_id)
  AND caller_authorization.account_id = sqlc.arg(account_id)
  AND caller_authorization.state = 'connected'
FOR UPDATE OF caller_authorization;

-- name: UpdateGitHubUserCredential :execrows
UPDATE realqa_github_connections
SET credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(connection_id)
  AND connected_by_account_id = sqlc.arg(account_id)
  AND state = 'connected';

-- name: UpdateGitHubCallerAuthorization :execrows
UPDATE realqa_github_user_authorizations
SET credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    updated_at = transaction_timestamp()
WHERE connection_id = sqlc.arg(connection_id)
  AND account_id = sqlc.arg(account_id)
  AND state = 'connected';

-- name: BeginGitHubUserCredentialRefresh :one
UPDATE realqa_github_connections
SET state = 'disconnected',
    connected_by_account_id = NULL,
    credential_ciphertext = NULL,
    wrapped_data_key = NULL,
    key_id = NULL,
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    updated_at = transaction_timestamp()
WHERE id = sqlc.arg(connection_id)
  AND connected_by_account_id = sqlc.arg(account_id)
  AND state = 'connected'
RETURNING revision;

-- name: BeginGitHubCallerAuthorizationRefresh :one
WITH disconnected_authorization AS (
    UPDATE realqa_github_user_authorizations AS caller_authorization
    SET state = 'disconnected',
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        updated_at = transaction_timestamp()
    WHERE caller_authorization.connection_id = sqlc.arg(connection_id)
      AND caller_authorization.account_id = sqlc.arg(account_id)
      AND caller_authorization.state = 'connected'
    RETURNING
        caller_authorization.revision,
        caller_authorization.connection_id,
        caller_authorization.account_id
),
cleared_repository_access AS (
    DELETE FROM realqa_repository_access AS access
    USING realqa_github_installations AS installation,
          disconnected_authorization
    WHERE access.installation_id = installation.id
      AND installation.connection_id =
          disconnected_authorization.connection_id
      AND access.account_id = disconnected_authorization.account_id
    RETURNING 1
)
SELECT revision
FROM disconnected_authorization;

-- name: CompleteGitHubUserCredentialRefresh :execrows
UPDATE realqa_github_connections AS connection
SET state = 'connected',
    connected_by_account_id = sqlc.arg(account_id),
    credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    updated_at = transaction_timestamp()
WHERE connection.id = sqlc.arg(connection_id)
  AND connection.revision = sqlc.arg(expected_revision)
  AND connection.state = 'disconnected'
  AND connection.credential_ciphertext IS NULL
  AND connection.wrapped_data_key IS NULL
  AND connection.key_id IS NULL
  AND connection.oauth_state_digest IS NULL
  AND connection.oauth_state_expires_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM realqa_identities AS identity
      WHERE identity.account_id = sqlc.arg(account_id)
        AND identity.deleted_at IS NULL
  );

-- name: CompleteGitHubCallerAuthorizationRefresh :execrows
UPDATE realqa_github_user_authorizations AS caller_authorization
SET state = 'connected',
    credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    updated_at = transaction_timestamp()
WHERE caller_authorization.connection_id = sqlc.arg(connection_id)
  AND caller_authorization.account_id = sqlc.arg(account_id)
  AND caller_authorization.revision = sqlc.arg(expected_revision)
  AND caller_authorization.state = 'disconnected'
  AND caller_authorization.credential_ciphertext IS NULL
  AND caller_authorization.wrapped_data_key IS NULL
  AND caller_authorization.key_id IS NULL
  AND caller_authorization.oauth_state_digest IS NULL
  AND caller_authorization.oauth_state_expires_at IS NULL
  AND EXISTS (
      SELECT 1
      FROM realqa_identities AS identity
      WHERE identity.account_id = sqlc.arg(account_id)
        AND identity.deleted_at IS NULL
  );

-- name: ConnectGitHubCallerAuthorization :execrows
UPDATE realqa_github_user_authorizations AS caller_authorization
SET state = 'connected',
    github_login = sqlc.arg(github_login),
    github_user_id = sqlc.arg(github_user_id),
    credential_ciphertext = sqlc.arg(credential_ciphertext),
    wrapped_data_key = sqlc.arg(wrapped_data_key),
    key_id = sqlc.arg(key_id),
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    connected_at = transaction_timestamp(),
    revision = caller_authorization.revision + 1,
    updated_at = transaction_timestamp()
FROM realqa_github_connections AS connection
WHERE connection.id = caller_authorization.connection_id
  AND connection.owner_kind = sqlc.arg(owner_kind)
  AND connection.owner_id = sqlc.arg(owner_id)
  AND connection.state = 'connected'
  AND caller_authorization.account_id = sqlc.arg(account_id)
  AND caller_authorization.oauth_state_digest = sqlc.arg(oauth_state_digest)
  AND caller_authorization.oauth_state_expires_at > transaction_timestamp();

-- name: GitHubInstallationIsActiveForOwner :one
SELECT EXISTS (
    SELECT 1
    FROM realqa_github_installations AS installation
    JOIN realqa_github_connections AS connection
      ON connection.id = installation.connection_id
     AND connection.state = 'connected'
    WHERE installation.provider_installation_id = sqlc.arg(provider_installation_id)
      AND installation.owner_kind = sqlc.arg(owner_kind)
      AND installation.owner_id = sqlc.arg(owner_id)
      AND installation.state = 'active'
);

-- name: DeleteRepositoryAccessForAccount :execrows
DELETE FROM realqa_repository_access
WHERE installation_id = sqlc.arg(installation_id)
  AND account_id = sqlc.arg(account_id);

-- name: UpsertRepositoryAccess :exec
INSERT INTO realqa_repository_access (
    installation_id, account_id, repository_id, repository_owner,
    repository_name, issues_enabled, can_submit
)
VALUES (
    sqlc.arg(installation_id), sqlc.arg(account_id), sqlc.arg(repository_id),
    sqlc.arg(repository_owner), sqlc.arg(repository_name),
    sqlc.arg(issues_enabled), sqlc.arg(can_submit)
)
ON CONFLICT (installation_id, account_id, repository_id)
DO UPDATE SET repository_owner = EXCLUDED.repository_owner,
              repository_name = EXCLUDED.repository_name,
              issues_enabled = EXCLUDED.issues_enabled,
              can_submit = EXCLUDED.can_submit,
              checked_at = transaction_timestamp();

-- name: DeleteRepositoryDefinitions :execrows
DELETE FROM realqa_repository_definitions
WHERE installation_id = sqlc.arg(installation_id)
  AND repository_id = sqlc.arg(repository_id);

-- name: GetRepositorySchemaRevision :one
SELECT revision
FROM realqa_repository_schema_revisions
WHERE installation_id = sqlc.arg(installation_id)
  AND repository_id = sqlc.arg(repository_id);

-- name: BumpRepositorySchemaRevision :one
INSERT INTO realqa_repository_schema_revisions (
    installation_id, repository_id, revision
)
VALUES (
    sqlc.arg(installation_id), sqlc.arg(repository_id), 1
)
ON CONFLICT (installation_id, repository_id)
DO UPDATE SET revision = realqa_repository_schema_revisions.revision + 1,
              updated_at = transaction_timestamp()
RETURNING revision;

-- name: UpsertRepositoryDefinition :exec
INSERT INTO realqa_repository_definitions (
    installation_id, repository_id, kind, definition_id, name, path, etag,
    schema_payload
)
VALUES (
    sqlc.arg(installation_id), sqlc.arg(repository_id), sqlc.arg(kind),
    sqlc.arg(definition_id), sqlc.arg(name), sqlc.arg(path), sqlc.arg(etag),
    sqlc.arg(schema_payload)
)
ON CONFLICT (installation_id, repository_id, kind, definition_id)
DO UPDATE SET name = EXCLUDED.name,
              path = EXCLUDED.path,
              etag = EXCLUDED.etag,
              schema_payload = EXCLUDED.schema_payload,
              revision = realqa_repository_definitions.revision + 1,
              updated_at = transaction_timestamp();

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

-- name: RenameGitHubInstallationAccount :one
WITH renamed_installation AS (
    UPDATE realqa_github_installations
    SET provider_account_id = sqlc.arg(provider_account_id),
        account_login = sqlc.arg(account_login),
        account_kind = sqlc.arg(account_kind),
        revision = revision + 1,
        updated_at = transaction_timestamp()
    WHERE provider_installation_id = sqlc.arg(provider_installation_id)
      AND state <> 'deleted'
    RETURNING id
),
renamed_destinations AS (
    UPDATE realqa_destinations
    SET repository_owner = sqlc.arg(account_login)
    WHERE installation_id IN (SELECT id FROM renamed_installation)
    RETURNING 1
),
renamed_repository_access AS (
    UPDATE realqa_repository_access
    SET repository_owner = sqlc.arg(account_login)
    WHERE installation_id IN (SELECT id FROM renamed_installation)
    RETURNING 1
)
SELECT count(*)::bigint
FROM renamed_installation;

-- name: SuspendUnauthorizedGitHubInstallations :execrows
UPDATE realqa_github_installations
SET state = 'suspended',
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id)
  AND state = 'active'
  AND provider_installation_id
      <> ALL(sqlc.arg(authorized_installation_ids)::bigint[]);

-- name: SetGitHubInstallationState :execrows
UPDATE realqa_github_installations
SET state = CASE
        WHEN state = 'deleted' THEN 'deleted'
        ELSE sqlc.arg(state)
    END,
    revision = CASE
        WHEN state = 'deleted' THEN revision
        ELSE revision + 1
    END,
    updated_at = CASE
        WHEN state = 'deleted' THEN updated_at
        ELSE transaction_timestamp()
    END
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

-- name: DisconnectGitHubUserCredentials :one
WITH disconnected AS (
    UPDATE realqa_github_connections AS connection
    SET state = 'disconnected',
        connected_by_account_id = NULL,
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = connection.revision + 1,
        updated_at = transaction_timestamp()
    WHERE connection.github_user_id = sqlc.arg(provider_user_id)
      AND connection.credential_ciphertext IS NOT NULL
    RETURNING connection.id
),
cleared_authorizations AS (
    UPDATE realqa_github_user_authorizations
    SET state = 'disconnected',
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = revision + 1,
        updated_at = transaction_timestamp()
    WHERE connection_id IN (SELECT id FROM disconnected)
    RETURNING 1
),
cleared_repository_access AS (
    DELETE FROM realqa_repository_access AS access
    USING realqa_github_installations AS installation
    WHERE access.installation_id = installation.id
      AND installation.connection_id IN (SELECT id FROM disconnected)
    RETURNING 1
)
SELECT count(*)::bigint
FROM disconnected;

-- name: DisconnectGitHubCallerAuthorizations :one
WITH disconnected_authorizations AS (
    UPDATE realqa_github_user_authorizations AS caller_authorization
    SET state = 'disconnected',
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = caller_authorization.revision + 1,
        updated_at = transaction_timestamp()
    WHERE caller_authorization.github_user_id = sqlc.arg(github_user_id)
      AND caller_authorization.credential_ciphertext IS NOT NULL
    RETURNING caller_authorization.connection_id, caller_authorization.account_id
),
cleared_repository_access AS (
    DELETE FROM realqa_repository_access AS access
    USING realqa_github_installations AS installation,
          disconnected_authorizations AS disconnected_authorization
    WHERE access.installation_id = installation.id
      AND installation.connection_id = disconnected_authorization.connection_id
      AND access.account_id = disconnected_authorization.account_id
    RETURNING 1
)
SELECT count(*)::bigint
FROM disconnected_authorizations;

-- name: DisconnectGitHubCallerAuthorizationsForConnection :one
WITH disconnected_authorizations AS (
    UPDATE realqa_github_user_authorizations AS caller_authorization
    SET state = 'disconnected',
        credential_ciphertext = NULL,
        wrapped_data_key = NULL,
        key_id = NULL,
        oauth_state_digest = NULL,
        oauth_state_expires_at = NULL,
        revision = caller_authorization.revision + 1,
        updated_at = transaction_timestamp()
    WHERE caller_authorization.connection_id = sqlc.arg(target_connection_id)
      AND (
          caller_authorization.credential_ciphertext IS NOT NULL
          OR caller_authorization.oauth_state_digest IS NOT NULL
      )
    RETURNING 1
),
cleared_repository_access AS (
    DELETE FROM realqa_repository_access AS access
    USING realqa_github_installations AS installation
    WHERE access.installation_id = installation.id
      AND installation.connection_id = sqlc.arg(target_connection_id)
    RETURNING 1
)
SELECT count(*)::bigint
FROM disconnected_authorizations;
