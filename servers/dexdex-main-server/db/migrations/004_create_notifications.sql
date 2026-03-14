-- +goose Up
CREATE TABLE notifications (
    notification_id TEXT PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    type INTEGER NOT NULL DEFAULT 0,
    title TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    reference_id TEXT NOT NULL DEFAULT '',
    read BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX idx_notifications_workspace ON notifications(workspace_id);

-- +goose Down
DROP TABLE IF EXISTS notifications;
