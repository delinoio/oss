-- name: GetPullRequest :one
SELECT * FROM pr_records WHERE workspace_id = $1 AND pr_tracking_id = $2;

-- name: ListPullRequests :many
SELECT * FROM pr_records WHERE workspace_id = $1;

-- name: CreatePullRequest :one
INSERT INTO pr_records (
    pr_tracking_id,
    workspace_id,
    status,
    pr_url,
    unit_task_id,
    auto_fix_enabled,
    fix_attempt_count,
    max_fix_attempts,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (workspace_id, pr_tracking_id)
DO UPDATE SET
    status = EXCLUDED.status,
    pr_url = EXCLUDED.pr_url,
    unit_task_id = EXCLUDED.unit_task_id,
    auto_fix_enabled = EXCLUDED.auto_fix_enabled,
    fix_attempt_count = EXCLUDED.fix_attempt_count,
    max_fix_attempts = EXCLUDED.max_fix_attempts,
    updated_at = EXCLUDED.updated_at
RETURNING *;

-- name: UpdatePullRequestStatus :one
UPDATE pr_records
SET status = $3, updated_at = NOW()
WHERE workspace_id = $1 AND pr_tracking_id = $2
RETURNING *;

-- name: UpdatePullRequestAutoFixPolicy :one
UPDATE pr_records
SET auto_fix_enabled = $3, updated_at = NOW()
WHERE workspace_id = $1 AND pr_tracking_id = $2
RETURNING *;

-- name: UpdatePullRequestFixAttemptCount :one
UPDATE pr_records
SET fix_attempt_count = $3, updated_at = NOW()
WHERE workspace_id = $1 AND pr_tracking_id = $2
RETURNING *;
