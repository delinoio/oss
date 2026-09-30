// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration007(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, executionSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const executionSchema = `
CREATE TABLE execution_grants (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 digest BLOB NOT NULL UNIQUE CHECK(length(digest)=32),
 execution_id TEXT NOT NULL, machine_id TEXT NOT NULL,
 instance_id TEXT NOT NULL, device_id TEXT NOT NULL, server_epoch TEXT NOT NULL
);
CREATE TABLE execution_references (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 account_id TEXT NOT NULL, connection_id TEXT NOT NULL, model_id TEXT NOT NULL,
 reference_kind TEXT NOT NULL, native_id TEXT NOT NULL,
 PRIMARY KEY(session_id,account_id,connection_id,model_id,reference_kind,native_id)
);
PRAGMA user_version=7;
`
