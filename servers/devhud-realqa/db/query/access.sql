-- name: GetIdentityBySubjectDigest :one
SELECT *
FROM realqa_identities
WHERE subject_digest = sqlc.arg(subject_digest)
  AND deleted_at IS NULL;

-- name: GetOwnerAccess :one
SELECT binding.*
FROM realqa_owner_bindings AS binding
JOIN realqa_identities AS identity ON identity.account_id = binding.account_id
LEFT JOIN realqa_scope_tombstones AS tombstone
  ON tombstone.owner_kind = binding.owner_kind
 AND tombstone.owner_id = binding.owner_id
WHERE binding.account_id = sqlc.arg(account_id)
  AND binding.owner_kind = sqlc.arg(owner_kind)
  AND binding.owner_id = sqlc.arg(owner_id)
  AND identity.deleted_at IS NULL
  AND tombstone.owner_id IS NULL
FOR SHARE OF binding;

-- name: ScopeIsTombstoned :one
SELECT EXISTS (
    SELECT 1
    FROM realqa_scope_tombstones
    WHERE owner_kind = sqlc.arg(owner_kind)
      AND owner_id = sqlc.arg(owner_id)
);

-- name: HasPayerTeamAccess :one
SELECT EXISTS (
    SELECT 1
    FROM realqa_payer_team_bindings AS binding
    LEFT JOIN realqa_scope_tombstones AS tombstone
      ON tombstone.owner_kind = 'organization'
     AND tombstone.owner_id = binding.organization_id
    WHERE binding.account_id = sqlc.arg(account_id)
      AND binding.organization_id = sqlc.arg(organization_id)
      AND binding.team_id = sqlc.arg(team_id)
      AND tombstone.owner_id IS NULL
);

-- name: GetRepositorySubmitAccess :one
SELECT access.*
FROM realqa_repository_access AS access
JOIN realqa_github_installations AS installation
  ON installation.id = access.installation_id
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE access.installation_id = sqlc.arg(installation_id)
  AND access.account_id = sqlc.arg(account_id)
  AND access.repository_id = sqlc.arg(repository_id)
  AND access.issues_enabled
  AND access.can_submit
  AND access.checked_at >= statement_timestamp() - interval '5 minutes';

-- name: GetRepositorySubmitAccessForOwner :one
SELECT access.*
FROM realqa_repository_access AS access
JOIN realqa_github_installations AS installation
  ON installation.id = access.installation_id
JOIN realqa_github_connections AS connection
  ON connection.id = installation.connection_id
 AND connection.state = 'connected'
WHERE access.installation_id = sqlc.arg(installation_id)
  AND installation.owner_kind = sqlc.arg(owner_kind)
  AND installation.owner_id = sqlc.arg(owner_id)
  AND access.account_id = sqlc.arg(account_id)
  AND access.repository_id = sqlc.arg(repository_id)
  AND access.issues_enabled
  AND access.can_submit
  AND access.checked_at >= statement_timestamp() - interval '5 minutes'
FOR SHARE OF connection;

-- name: LockShortcutAccount :exec
SELECT pg_advisory_xact_lock(
    hashtextextended(
        'shortcut:' || sqlc.arg(account_id)::uuid::text,
        757
    )
);

-- name: CountActiveShortcutsForAccount :one
SELECT count(*)::bigint
FROM realqa_shortcuts AS shortcut
JOIN realqa_presets AS preset ON preset.id = shortcut.preset_id
WHERE preset.created_by_account_id = sqlc.arg(account_id)
  AND shortcut.active;

-- name: CountOtherActiveShortcutsForAccount :one
SELECT count(*)::bigint
FROM realqa_shortcuts AS shortcut
JOIN realqa_presets AS preset ON preset.id = shortcut.preset_id
WHERE preset.created_by_account_id = sqlc.arg(account_id)
  AND preset.id <> sqlc.arg(preset_id)
  AND shortcut.active;
