-- +goose Up
CREATE TABLE sub_tasks (
    sub_task_id TEXT PRIMARY KEY,
    unit_task_id TEXT NOT NULL REFERENCES unit_tasks(unit_task_id),
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    type INTEGER NOT NULL DEFAULT 0,
    status INTEGER NOT NULL DEFAULT 0,
    completion_reason INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL DEFAULT '',
    session_id TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_sub_tasks_workspace ON sub_tasks(workspace_id);
CREATE INDEX idx_sub_tasks_unit_task ON sub_tasks(unit_task_id);

-- +goose Down
DROP TABLE IF EXISTS sub_tasks;
