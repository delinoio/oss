// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration006(ctx context.Context, tx *sql.Tx, _ int) error {
	if _, err := tx.ExecContext(ctx, assignmentSchema); err != nil {
		return storageError(err)
	}
	original := &Tx{tx: tx, ctx: ctx}
	if err := original.preserveLegacyAssignments(); err != nil {
		return err
	}
	return nil
}

const assignmentSchema = `
CREATE TABLE job_assignments (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, session_id TEXT NOT NULL, project_id TEXT NOT NULL,
 body BLOB NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
PRAGMA user_version=6;
`
