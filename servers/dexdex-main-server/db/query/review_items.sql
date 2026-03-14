-- name: ListReviewAssistItems :many
SELECT * FROM review_assist_items WHERE workspace_id = $1 AND unit_task_id = $2;

-- name: CreateReviewAssistItem :one
INSERT INTO review_assist_items (workspace_id, unit_task_id, review_assist_id, body)
VALUES ($1, $2, $3, $4)
RETURNING *;

-- name: ListReviewComments :many
SELECT * FROM review_comments WHERE workspace_id = $1 AND pr_tracking_id = $2;

-- name: CreateReviewComment :one
INSERT INTO review_comments (workspace_id, pr_tracking_id, review_comment_id, body)
VALUES ($1, $2, $3, $4)
RETURNING *;
