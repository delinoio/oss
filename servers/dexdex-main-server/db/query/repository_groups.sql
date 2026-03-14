-- name: GetRepositoryGroup :one
SELECT * FROM repository_groups WHERE workspace_id = $1 AND repository_group_id = $2;

-- name: ListRepositoryGroups :many
SELECT * FROM repository_groups WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: CreateRepositoryGroup :one
INSERT INTO repository_groups (repository_group_id, workspace_id, created_at, updated_at)
VALUES ($1, $2, NOW(), NOW())
RETURNING *;

-- name: TouchRepositoryGroup :exec
UPDATE repository_groups
SET updated_at = NOW()
WHERE workspace_id = $1 AND repository_group_id = $2;

-- name: DeleteRepositoryGroup :exec
DELETE FROM repository_groups WHERE workspace_id = $1 AND repository_group_id = $2;

-- name: DeleteRepositoryGroupMembers :exec
DELETE FROM repository_group_members WHERE workspace_id = $1 AND repository_group_id = $2;

-- name: CreateRepositoryGroupMember :one
INSERT INTO repository_group_members (
    workspace_id,
    repository_group_id,
    repository_id,
    branch_ref,
    display_order,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, NOW(), NOW())
RETURNING *;

-- name: ListRepositoryGroupMembers :many
SELECT * FROM repository_group_members
WHERE workspace_id = $1 AND repository_group_id = $2
ORDER BY display_order ASC;

-- name: CountRepositoryReferences :one
SELECT COUNT(*) FROM repository_group_members
WHERE workspace_id = $1 AND repository_id = $2;
