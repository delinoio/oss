// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration009(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, interactionSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const interactionSchema = `
CREATE TABLE execution_interactions (
 interaction_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL, native_thread_id TEXT NOT NULL, native_request_key TEXT NOT NULL,
 closure TEXT NOT NULL CHECK(closure IN ('open','native-closed','turn-ended')),
 question_bytes INTEGER NOT NULL CHECK(question_bytes > 0 AND question_bytes <= 524288),
 UNIQUE(execution_id,native_request_key)
);
CREATE INDEX execution_interaction_state ON execution_interactions(execution_id,closure);
PRAGMA user_version=9;
`
