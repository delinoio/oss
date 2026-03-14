-- +goose Up
CREATE TABLE unit_tasks (
    unit_task_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    status INTEGER NOT NULL DEFAULT 0,
    action_required INTEGER NOT NULL DEFAULT 0,
    prompt TEXT NOT NULL DEFAULT '',
    repository_group_id TEXT NOT NULL DEFAULT '',
    agent_cli_type INTEGER NOT NULL DEFAULT 0,
    use_plan_mode BOOLEAN NOT NULL DEFAULT FALSE,
    sub_task_count INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_unit_tasks_workspace ON unit_tasks(workspace_id);

-- +goose Down
DROP TABLE IF EXISTS unit_tasks;
