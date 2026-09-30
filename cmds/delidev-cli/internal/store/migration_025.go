// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration025(ctx context.Context, tx *sql.Tx, original int) error {
	_, err := tx.ExecContext(ctx, nativeAccountingSchema)
	return storageError(err)
}

const nativeAccountingSchema = `
INSERT INTO metadata(key,value) VALUES('native_accounting_layout','grok-closed-input-v1');
CREATE TABLE native_accounting (
 id TEXT PRIMARY KEY,
 kind INTEGER NOT NULL,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 input_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(kind,execution_id,input_id)
);
CREATE INDEX native_accounting_time ON native_accounting(created_at,id);
CREATE INDEX native_accounting_session ON native_accounting(session_id,created_at,id);
CREATE INDEX native_accounting_project ON native_accounting(project_id,created_at,id);
`
