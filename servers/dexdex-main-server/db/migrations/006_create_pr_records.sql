-- +goose Up
CREATE TABLE pr_records (
    pr_tracking_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    status INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (workspace_id, pr_tracking_id)
);

-- +goose Down
DROP TABLE IF EXISTS pr_records;
