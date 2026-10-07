// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

// Token bytes belong to the Vault. These rows retain generation and once-only
// refresh authority independently of account deletion and historical restore.
func migration031(ctx context.Context, tx *sql.Tx, original int) error {
	_, err := tx.ExecContext(ctx, accountOAuthCredentialSchema)
	return storageError(err)
}

const accountOAuthCredentialSchema = `
INSERT INTO metadata(key,value) VALUES('account_oauth_credentials_layout','token-generations-v1');
CREATE TABLE account_oauth_credentials (
 account_id TEXT NOT NULL,
 connection_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=8192),
 PRIMARY KEY(account_id,connection_id)
);
`
