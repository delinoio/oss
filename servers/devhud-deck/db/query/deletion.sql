-- name: GetDeletionJobByReplayKey :one
SELECT * FROM deck_deletion_jobs
WHERE replay_key = sqlc.arg(replay_key);

-- name: InsertOwnerTombstone :exec
INSERT INTO deck_owner_tombstones (target_hash, accepted_at)
VALUES (sqlc.arg(target_hash), sqlc.arg(accepted_at))
ON CONFLICT DO NOTHING;

-- name: InsertDeletionJob :one
INSERT INTO deck_deletion_jobs (
    deletion_job_id, replay_key, target_hash, trigger, state,
    accepted_at, completed_at
) VALUES (
    sqlc.arg(deletion_job_id), sqlc.arg(replay_key),
    sqlc.arg(target_hash), sqlc.arg(trigger), 3,
    sqlc.arg(accepted_at), sqlc.arg(accepted_at)
)
RETURNING *;

-- name: DeletePersonalFeatureData :exec
DELETE FROM deck_views
WHERE owner_scope = 1 AND owner_account_id = sqlc.arg(account_id);

-- name: DeleteOrganizationFeatureData :exec
DELETE FROM deck_views
WHERE owner_scope = 2 AND owner_organization_id = sqlc.arg(organization_id);

-- name: DeleteViewCreateIdempotencyByOwnerHash :exec
DELETE FROM deck_view_create_idempotency
WHERE owner_hash = sqlc.arg(owner_hash);

-- name: ListOrganizationViewIDsForUpdate :many
SELECT view_id
FROM deck_views
WHERE owner_scope = 2 AND owner_organization_id = sqlc.arg(organization_id)
ORDER BY view_id
FOR UPDATE;

-- name: ListDeviceRegistrationsForUpdate :many
SELECT *
FROM deck_device_registrations
ORDER BY registration_id
FOR UPDATE;

-- name: UpdateDeviceViewStateAfterDeletion :exec
UPDATE deck_device_registrations
SET shortcuts_ciphertext = sqlc.arg(shortcuts_ciphertext),
    widgets_ciphertext = sqlc.arg(widgets_ciphertext),
    revision = revision + 1,
    updated_at = sqlc.arg(updated_at)
WHERE registration_id = sqlc.arg(registration_id)
  AND revision = sqlc.arg(expected_revision);

-- name: DeleteAccountDevices :exec
DELETE FROM deck_device_registrations
WHERE account_id = sqlc.arg(account_id);

-- name: DeletePersonalConnection :exec
DELETE FROM deck_connections
WHERE owner_scope = 1 AND owner_id = sqlc.arg(account_id);

-- name: DeleteOrganizationConnection :exec
DELETE FROM deck_connections
WHERE owner_scope = 2 AND owner_id = sqlc.arg(organization_id);

-- name: DeleteDeckAccount :exec
DELETE FROM deck_accounts WHERE account_id = sqlc.arg(account_id);

-- name: DeleteOrganizationMemberships :exec
DELETE FROM deck_organization_memberships
WHERE organization_id = sqlc.arg(organization_id);

-- name: DeleteOrganizationTeamMemberships :exec
DELETE FROM deck_team_memberships
WHERE organization_id = sqlc.arg(organization_id);
