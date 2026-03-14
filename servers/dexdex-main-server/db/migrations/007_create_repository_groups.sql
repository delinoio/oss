-- +goose Up
CREATE TABLE repositories (
    repository_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    repository_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, repository_id)
);

CREATE TABLE repository_groups (
    repository_group_id TEXT NOT NULL,
    workspace_id TEXT NOT NULL REFERENCES workspaces(workspace_id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, repository_group_id)
);

CREATE TABLE repository_group_members (
    workspace_id TEXT NOT NULL,
    repository_group_id TEXT NOT NULL,
    repository_id TEXT NOT NULL,
    branch_ref TEXT NOT NULL DEFAULT 'main',
    display_order INTEGER NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (workspace_id, repository_group_id, repository_id),
    UNIQUE (workspace_id, repository_group_id, display_order),
    FOREIGN KEY (workspace_id, repository_group_id)
        REFERENCES repository_groups(workspace_id, repository_group_id)
        ON DELETE CASCADE,
    FOREIGN KEY (workspace_id, repository_id)
        REFERENCES repositories(workspace_id, repository_id)
        ON DELETE RESTRICT
);

-- +goose Down
DROP TABLE IF EXISTS repository_group_members;
DROP TABLE IF EXISTS repository_groups;
DROP TABLE IF EXISTS repositories;
