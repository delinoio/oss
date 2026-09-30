// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration023(ctx context.Context, tx *sql.Tx, original int) error {
	// A known title-only version 23 needs the backup table; preserve existing rows.
	var exists bool
	if err := tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='backup_deletions')").Scan(&exists); err != nil {
		return storageError(err)
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, backupDeletionSchema); err != nil {
			return storageError(err)
		}
	}
	return nil
}

const backupDeletionSchema = `
CREATE TABLE backup_deletions (
 backup_id TEXT PRIMARY KEY,
 job_id TEXT NOT NULL UNIQUE REFERENCES entities(id),
 request_id TEXT NOT NULL UNIQUE
);
PRAGMA user_version=23;
`
