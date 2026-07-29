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

