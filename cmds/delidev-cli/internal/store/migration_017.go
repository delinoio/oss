// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration017(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, notificationSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const notificationSchema = `
CREATE TABLE notification_preferences (
 client_id TEXT PRIMARY KEY,
 revision INTEGER NOT NULL CHECK(revision>1),
 interactions INTEGER NOT NULL CHECK(interactions IN (0,1)),
 terminals INTEGER NOT NULL CHECK(terminals IN (0,1))
);
CREATE TABLE notification_deliveries (
 client_id TEXT NOT NULL,
 inbox_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 claim_id TEXT NOT NULL UNIQUE,
 kind TEXT NOT NULL CHECK(kind IN ('request','succeeded','failed','stopped')),
 state TEXT NOT NULL CHECK(state IN ('claimed','submitted','denied','failed','uncertain')),
 PRIMARY KEY(client_id,inbox_id)
);
PRAGMA user_version=17;
`
