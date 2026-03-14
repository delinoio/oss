-- +goose Up
CREATE TABLE review_assist_items (
    id SERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    unit_task_id TEXT NOT NULL,
    review_assist_id TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_review_assist_items_workspace_task ON review_assist_items(workspace_id, unit_task_id);

CREATE TABLE review_comments (
    id SERIAL PRIMARY KEY,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    pr_tracking_id TEXT NOT NULL,
    review_comment_id TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT ''
);

CREATE INDEX idx_review_comments_workspace_pr ON review_comments(workspace_id, pr_tracking_id);

-- +goose Down
DROP TABLE IF EXISTS review_comments;
DROP TABLE IF EXISTS review_assist_items;
