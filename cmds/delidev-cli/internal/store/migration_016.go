// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration016(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, budgetSchema); err != nil {
		return storageError(err)
	}
	if err := (&Tx{tx: tx, ctx: ctx}).backfillSessionEstimates(); err != nil {
		return err
	}
	return nil
}

const budgetSchema = `
CREATE TABLE session_estimate_totals (
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 currency TEXT NOT NULL,
 known_amount TEXT NOT NULL CHECK(length(known_amount)<=144),
 complete_responses INTEGER NOT NULL CHECK(complete_responses>=0),
 partial_responses INTEGER NOT NULL CHECK(partial_responses>=0),
 unavailable_responses INTEGER NOT NULL CHECK(unavailable_responses>=0),
 PRIMARY KEY(session_id,currency)
);
PRAGMA user_version=16;
`
