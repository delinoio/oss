// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration008(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, executionMessageSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const executionMessageSchema = `
CREATE TABLE execution_messages (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 execution_id TEXT NOT NULL,
 native_thread_id TEXT NOT NULL, native_turn_id TEXT NOT NULL, native_item_id TEXT NOT NULL,
 message_id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 state TEXT NOT NULL CHECK(state IN ('streaming','complete')),
 PRIMARY KEY(session_id,native_thread_id,native_turn_id,native_item_id)
);
CREATE INDEX execution_message_count ON execution_messages(execution_id,state);
PRAGMA user_version=8;
`
