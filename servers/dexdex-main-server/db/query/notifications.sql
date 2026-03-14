-- name: ListNotifications :many
SELECT * FROM notifications WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: MarkNotificationRead :one
UPDATE notifications SET read = TRUE
WHERE workspace_id = $1 AND notification_id = $2
RETURNING *;

-- name: CreateNotification :one
INSERT INTO notifications (notification_id, workspace_id, type, title, body, reference_id, read, created_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING *;
