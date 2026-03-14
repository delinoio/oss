-- name: GetUnitTask :one
SELECT * FROM unit_tasks WHERE workspace_id = $1 AND unit_task_id = $2;

-- name: ListUnitTasks :many
SELECT * FROM unit_tasks WHERE workspace_id = $1 ORDER BY created_at DESC;

-- name: CreateUnitTask :one
INSERT INTO unit_tasks (
    unit_task_id,
    workspace_id,
    status,
    prompt,
    repository_group_id,
    agent_cli_type,
    use_plan_mode,
    created_at,
    updated_at
)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
RETURNING *;

-- name: UpdateUnitTaskStatus :one
UPDATE unit_tasks SET status = $3, updated_at = NOW()
WHERE workspace_id = $1 AND unit_task_id = $2
RETURNING *;
