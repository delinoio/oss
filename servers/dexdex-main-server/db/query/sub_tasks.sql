-- name: GetSubTask :one
SELECT * FROM sub_tasks WHERE workspace_id = $1 AND sub_task_id = $2;

-- name: ListSubTasks :many
SELECT * FROM sub_tasks WHERE workspace_id = $1 AND unit_task_id = $2 ORDER BY created_at;

-- name: UpsertSubTask :one
INSERT INTO sub_tasks (sub_task_id, unit_task_id, workspace_id, type, status, completion_reason, title, session_id, created_at, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
ON CONFLICT (sub_task_id) DO UPDATE SET
    type = EXCLUDED.type,
    status = EXCLUDED.status,
    completion_reason = EXCLUDED.completion_reason,
    title = EXCLUDED.title,
    session_id = EXCLUDED.session_id,
    updated_at = EXCLUDED.updated_at
RETURNING *;
