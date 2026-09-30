// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration005(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, jobControlSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const jobControlSchema = `
CREATE TABLE job_cancellations (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 requested_at INTEGER NOT NULL
);
PRAGMA user_version=5;
`
