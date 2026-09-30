// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
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

func migration002(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, workerSchema); err != nil {
		return storageError(err)
	}
	return nil
}
