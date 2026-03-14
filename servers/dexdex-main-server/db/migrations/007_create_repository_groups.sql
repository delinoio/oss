-- +goose Up
CREATE TABLE repository_groups (
    repository_group_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    repositories JSONB NOT NULL DEFAULT '[]',
    PRIMARY KEY (workspace_id, repository_group_id)
);

-- +goose Down
DROP TABLE IF EXISTS repository_groups;
