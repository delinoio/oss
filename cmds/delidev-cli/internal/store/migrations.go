package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

const workerSchema = `
CREATE TABLE credential_verifiers (
 digest BLOB PRIMARY KEY CHECK(length(digest)=32),
 device_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE
);
CREATE TABLE pairing_verifiers (
 pairing_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 digest BLOB NOT NULL UNIQUE CHECK(length(digest)=32)
);
CREATE TABLE worker_instances (
 machine_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 instance_id TEXT NOT NULL, last_seen INTEGER NOT NULL
);
CREATE TABLE jobs (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 machine_id TEXT NOT NULL, parent_id TEXT NOT NULL, state TEXT NOT NULL,
 accepted_at INTEGER NOT NULL
);
CREATE INDEX jobs_dispatch ON jobs(machine_id,state,accepted_at,id);
CREATE INDEX jobs_parent ON jobs(parent_id,accepted_at,id);
PRAGMA user_version=2;
`

func migrate(ctx context.Context, db *sql.DB, root string) error {
	var version int
	if err := db.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return storageError(err)
	}
	if version == SchemaVersion {
		return nil
	}
	if version < 1 || version > 8 {
		return domain.Fail(domain.RecoveryRequired, "No supported migration exists for this database.", "Preserve the original and use a matching server version.")
	}
	// Back up even this additive migration. Destructive future migrations must
	// retain the same pre-migration backup boundary and never reset on failure.
	backup := filepath.Join(root, "backups", string(domain.NewID())+".sqlite")
	private, err := os.OpenFile(backup, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return storageError(err)
	}
	if err := private.Close(); err != nil {
		return storageError(err)
	}
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", backup); err != nil {
		return storageError(err)
	}
	if err := ValidateBackup(ctx, backup); err != nil {
		return err
	}
	file, err := os.OpenFile(backup, os.O_RDWR, 0)
	if err != nil {
		return storageError(err)
	}
	err = file.Sync()
	closeErr := file.Close()
	if err != nil {
		return storageError(err)
	}
	if closeErr != nil {
		return storageError(closeErr)
	}
	if err := security.SyncParent(backup); err != nil {
		return storageError(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return storageError(err)
	}
	defer tx.Rollback()
	if version == 1 {
		if _, err := tx.ExecContext(ctx, workerSchema); err != nil {
			return storageError(err)
		}
	}
	if version < 3 {
		if _, err := tx.ExecContext(ctx, catalogSchema); err != nil {
			return storageError(err)
		}
	}
	if version < 4 {
		if _, err := tx.ExecContext(ctx, sessionSchema); err != nil {
			return storageError(err)
		}
	}
	if version < 5 {
		if _, err := tx.ExecContext(ctx, jobControlSchema); err != nil {
			return storageError(err)
		}
	}
	if version < 6 {
		if _, err := tx.ExecContext(ctx, assignmentSchema); err != nil {
			return storageError(err)
		}
		original := &Tx{tx: tx, ctx: ctx}
		if err := original.preserveLegacyAssignments(); err != nil {
			return err
		}
	}
	if version < 7 {
		if _, err := tx.ExecContext(ctx, executionSchema); err != nil {
			return storageError(err)
		}
	}
	if version < 8 {
		if _, err := tx.ExecContext(ctx, executionMessageSchema); err != nil {
			return storageError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, interactionSchema); err != nil {
		return storageError(err)
	}
	return storageError(tx.Commit())
}
