-- +goose Up
ALTER TABLE workspaces
    ADD COLUMN IF NOT EXISTS type INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE workspaces
    DROP COLUMN IF EXISTS type;
