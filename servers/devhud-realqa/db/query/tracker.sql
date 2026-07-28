-- name: GetGitHubConnectionForOwner :one
SELECT *
FROM realqa_github_connections
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id);

-- name: StartGitHubConnection :one
INSERT INTO realqa_github_connections (
    id, owner_kind, owner_id, state, oauth_state_digest, oauth_state_expires_at
)
VALUES (
    sqlc.arg(id), sqlc.arg(owner_kind), sqlc.arg(owner_id), 'pending',
    sqlc.arg(oauth_state_digest), sqlc.arg(oauth_state_expires_at)
)
ON CONFLICT (owner_kind, owner_id)
DO UPDATE SET state = 'pending',
              credential_ciphertext = NULL,
              wrapped_data_key = NULL,
              key_id = NULL,
              oauth_state_digest = EXCLUDED.oauth_state_digest,
              oauth_state_expires_at = EXCLUDED.oauth_state_expires_at,
              revision = realqa_github_connections.revision + 1,
              updated_at = transaction_timestamp()
RETURNING *;

-- name: ListGitHubInstallations :many
SELECT installation.*
FROM realqa_github_installations AS installation
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE installation.owner_kind = sqlc.arg(owner_kind)
  AND installation.owner_id = sqlc.arg(owner_id)
  AND installation.id > sqlc.arg(after_id)
ORDER BY installation.id
LIMIT sqlc.arg(page_limit);

-- name: GetGitHubInstallation :one
SELECT installation.*
FROM realqa_github_installations AS installation
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE installation.id = sqlc.arg(id);

-- name: DisconnectGitHubConnection :one
UPDATE realqa_github_connections
SET state = 'disconnected',
    credential_ciphertext = NULL,
    wrapped_data_key = NULL,
    key_id = NULL,
    oauth_state_digest = NULL,
    oauth_state_expires_at = NULL,
    revision = revision + 1,
    updated_at = transaction_timestamp()
WHERE owner_kind = sqlc.arg(owner_kind)
  AND owner_id = sqlc.arg(owner_id)
  AND revision = sqlc.arg(expected_revision)
RETURNING *;

-- name: ListAccessibleRepositories :many
SELECT access.*
FROM realqa_repository_access AS access
JOIN realqa_github_installations AS installation
  ON installation.id = access.installation_id
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE access.installation_id = sqlc.arg(installation_id)
  AND access.account_id = sqlc.arg(account_id)
  AND (
      sqlc.arg(query)::text = ''
      OR access.repository_owner ILIKE '%' || sqlc.arg(query) || '%'
      OR access.repository_name ILIKE '%' || sqlc.arg(query) || '%'
  )
  AND access.repository_id > sqlc.arg(after_id)
ORDER BY access.repository_id
LIMIT sqlc.arg(page_limit);

-- name: ListRepositoryDefinitions :many
SELECT *
FROM realqa_repository_definitions
WHERE installation_id = sqlc.arg(installation_id)
  AND repository_id = sqlc.arg(repository_id)
ORDER BY kind, definition_id;
