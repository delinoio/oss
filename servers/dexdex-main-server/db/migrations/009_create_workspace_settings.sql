-- +goose Up
CREATE TABLE workspace_settings (
    workspace_id TEXT PRIMARY KEY REFERENCES workspaces(workspace_id),
    default_agent_cli_type INTEGER NOT NULL DEFAULT 2,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- +goose Down
DROP TABLE IF EXISTS workspace_settings;
