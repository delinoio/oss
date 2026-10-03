// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

// Rebuild the shared ledger without reinterpreting Grok bodies or backfilling
// old raw observations. A step's original source, not its input, is unique.
func migration026(ctx context.Context, tx *sql.Tx, original int) error {
	_, err := tx.ExecContext(ctx, pricedNativeAccountingSchema)
	return storageError(err)
}

const pricedNativeAccountingSchema = `
UPDATE metadata SET value='priced-native-input-v2' WHERE key='native_accounting_layout';
ALTER TABLE native_accounting RENAME TO native_accounting_25;
DROP INDEX native_accounting_time;
DROP INDEX native_accounting_session;
DROP INDEX native_accounting_project;
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
 source_id TEXT REFERENCES entities(id) ON DELETE CASCADE,
 pricing_id TEXT REFERENCES pricing_versions(id),
 estimate BLOB CHECK(length(estimate)<=16384)
);
INSERT INTO native_accounting(id,kind,session_id,project_id,execution_id,input_id,account_id,provider_id,model_id,body,created_at)
SELECT id,kind,session_id,project_id,execution_id,input_id,account_id,provider_id,model_id,body,created_at FROM native_accounting_25;
DROP TABLE native_accounting_25;
CREATE UNIQUE INDEX native_accounting_grok_input ON native_accounting(kind,execution_id,input_id) WHERE kind=2;
CREATE UNIQUE INDEX native_accounting_source ON native_accounting(source_id) WHERE source_id IS NOT NULL;
CREATE INDEX native_accounting_time ON native_accounting(created_at,id);
CREATE INDEX native_accounting_session ON native_accounting(session_id,created_at,id);
CREATE INDEX native_accounting_project ON native_accounting(project_id,created_at,id);
CREATE TABLE session_native_estimate_totals (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 currency TEXT NOT NULL,
 known_amount TEXT NOT NULL CHECK(length(known_amount)<=144),
 complete_units INTEGER NOT NULL CHECK(complete_units>=0),
 partial_units INTEGER NOT NULL CHECK(partial_units>=0),
 unavailable_units INTEGER NOT NULL CHECK(unavailable_units>=0),
 PRIMARY KEY(session_id,currency)
);
`
