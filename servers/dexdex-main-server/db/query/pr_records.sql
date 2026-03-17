-- name: GetPullRequest :one
SELECT * FROM pr_records WHERE workspace_id = $1 AND pr_tracking_id = $2;

-- name: ListPullRequests :many
SELECT * FROM pr_records WHERE workspace_id = $1;

-- name: CreatePullRequest :one
INSERT INTO pr_records (pr_tracking_id, workspace_id, status)
VALUES ($1, $2, $3)
RETURNING *;

-- name: UpdatePullRequestStatus :one
UPDATE pr_records
SET status = $3
WHERE workspace_id = $1 AND pr_tracking_id = $2
RETURNING *;
