-- name: GetWorkspace :one
SELECT * FROM workspaces WHERE workspace_id = $1;

-- name: ListWorkspaces :many
SELECT * FROM workspaces ORDER BY created_at;

-- name: CreateWorkspace :one
INSERT INTO workspaces (workspace_id, name, created_at)
VALUES ($1, $2, $3)
RETURNING *;
