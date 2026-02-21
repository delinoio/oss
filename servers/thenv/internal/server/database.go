package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	_ "modernc.org/sqlite"
)

func openDatabase(ctx context.Context, databasePath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", databasePath)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	if _, err := db.ExecContext(ctx, `PRAGMA foreign_keys = ON;`); err != nil {
		return nil, fmt.Errorf("enable foreign keys: %w", err)
	}
	if err := runMigrations(ctx, db); err != nil {
		return nil, err
	}
	return db, nil
}

func runMigrations(ctx context.Context, db *sql.DB) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS bundle_versions (
			bundle_version_id TEXT PRIMARY KEY,
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			status INTEGER NOT NULL,
			created_by TEXT NOT NULL,
			created_at TEXT NOT NULL,
			source_version_id TEXT,
			metadata TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS bundle_versions_scope_created_idx
		 ON bundle_versions(workspace_id, project_id, environment_id, created_at DESC);`,
		`CREATE TABLE IF NOT EXISTS bundle_files (
			bundle_version_id TEXT NOT NULL,
			file_type INTEGER NOT NULL,
			ciphertext BLOB NOT NULL,
			wrapped_dek BLOB NOT NULL,
			payload_nonce BLOB NOT NULL,
			dek_nonce BLOB NOT NULL,
			checksum TEXT NOT NULL,
			byte_length INTEGER NOT NULL,
			PRIMARY KEY(bundle_version_id, file_type),
			FOREIGN KEY(bundle_version_id) REFERENCES bundle_versions(bundle_version_id) ON DELETE CASCADE
		);`,
		`CREATE TABLE IF NOT EXISTS active_bundle_pointers (
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			bundle_version_id TEXT NOT NULL,
			updated_by TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(workspace_id, project_id, environment_id),
			FOREIGN KEY(bundle_version_id) REFERENCES bundle_versions(bundle_version_id)
		);`,
		`CREATE TABLE IF NOT EXISTS policy_bindings (
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			subject TEXT NOT NULL,
			role INTEGER NOT NULL,
			policy_revision INTEGER NOT NULL,
			updated_at TEXT NOT NULL,
			PRIMARY KEY(workspace_id, project_id, environment_id, subject)
		);`,
		`CREATE INDEX IF NOT EXISTS policy_bindings_scope_revision_idx
		 ON policy_bindings(workspace_id, project_id, environment_id, policy_revision DESC);`,
		`CREATE TABLE IF NOT EXISTS audit_events (
			event_id TEXT PRIMARY KEY,
			event_type INTEGER NOT NULL,
			actor TEXT NOT NULL,
			workspace_id TEXT NOT NULL,
			project_id TEXT NOT NULL,
			environment_id TEXT NOT NULL,
			target_bundle_version_id TEXT NOT NULL DEFAULT '',
			result TEXT NOT NULL,
			failure_code TEXT NOT NULL DEFAULT '',
			request_id TEXT NOT NULL DEFAULT '',
			trace_id TEXT NOT NULL DEFAULT '',
			created_at TEXT NOT NULL,
			metadata TEXT NOT NULL DEFAULT ''
		);`,
		`CREATE INDEX IF NOT EXISTS audit_events_scope_created_idx
		 ON audit_events(workspace_id, project_id, environment_id, created_at DESC);`,
	}

	for _, statement := range statements {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("run migration %q: %w", statement, err)
		}
	}
	return nil
}

func parseCursor(cursor string) (int, error) {
	if cursor == "" {
		return 0, nil
	}
	var value int
	_, err := fmt.Sscanf(cursor, "%d", &value)
	if err != nil || value < 0 {
		return 0, errors.New("cursor must be a non-negative integer")
	}
	return value, nil
}

func nextCursor(limit uint32, offset int, count int) string {
	if count <= int(limit) {
		return ""
	}
	return fmt.Sprintf("%d", offset+int(limit))
}
