-- name: GetRepositoryGroup :one
SELECT * FROM repository_groups WHERE workspace_id = $1 AND repository_group_id = $2;

-- name: ListRepositoryGroups :many
SELECT * FROM repository_groups WHERE workspace_id = $1;

-- name: CreateRepositoryGroup :one
INSERT INTO repository_groups (repository_group_id, workspace_id, repositories)
VALUES ($1, $2, $3)
RETURNING *;
