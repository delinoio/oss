-- +goose Up
CREATE TABLE session_summaries (
    session_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    parent_session_id TEXT NOT NULL DEFAULT '',
    root_session_id TEXT NOT NULL DEFAULT '',
    fork_status INTEGER NOT NULL DEFAULT 0,
    forked_from_sequence BIGINT NOT NULL DEFAULT 0,
    agent_session_status INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, session_id)
);

CREATE INDEX idx_session_summaries_parent ON session_summaries(workspace_id, parent_session_id);

-- +goose Down
DROP TABLE IF EXISTS session_summaries;
