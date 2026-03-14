-- name: GetRepository :one
SELECT * FROM repositories WHERE workspace_id = $1 AND repository_id = $2;

-- name: ListRepositories :many
SELECT * FROM repositories WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: CreateRepository :one
INSERT INTO repositories (
    repository_id,
    workspace_id,
    repository_url,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, NOW(), NOW())
RETURNING *;

-- name: UpdateRepository :one
UPDATE repositories
SET repository_url = $3, updated_at = NOW()
WHERE workspace_id = $1 AND repository_id = $2
RETURNING *;

-- name: DeleteRepository :exec
DELETE FROM repositories WHERE workspace_id = $1 AND repository_id = $2;
