// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

// OAuth attempts deliberately have no account foreign key: a minted protected
// reference can exist before metadata, and deletion cannot erase recovery proof.
func migration029(ctx context.Context, tx *sql.Tx, original int) error {
	_, err := tx.ExecContext(ctx, accountOAuthSchema)
	return storageError(err)
}

const accountOAuthSchema = `
INSERT INTO metadata(key,value) VALUES('account_oauth_layout','pkce-once-v1');
CREATE TABLE account_oauth_attempts (
 id TEXT PRIMARY KEY,
 revision INTEGER NOT NULL CHECK(revision>0),
 state TEXT NOT NULL,
 generation TEXT NOT NULL,
 start_request_id TEXT NOT NULL UNIQUE,
 body BLOB NOT NULL CHECK(length(body)<=8192)
);
CREATE INDEX account_oauth_lifetime ON account_oauth_attempts(generation,state);
`
