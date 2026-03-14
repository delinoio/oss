-- name: GetWorkspaceSettings :one
SELECT * FROM workspace_settings WHERE workspace_id = $1;

-- name: UpsertWorkspaceSettings :one
INSERT INTO workspace_settings (
    workspace_id,
    default_agent_cli_type,
    updated_at
)
VALUES ($1, $2, NOW())
ON CONFLICT (workspace_id)
DO UPDATE SET
    default_agent_cli_type = EXCLUDED.default_agent_cli_type,
    updated_at = NOW()
RETURNING *;
