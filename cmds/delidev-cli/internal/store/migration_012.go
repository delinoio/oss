// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration012(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, deletedConfigurationSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const deletedConfigurationSchema = `
CREATE TABLE deleted_project_policies (
 project_id TEXT PRIMARY KEY REFERENCES tombstones(id),
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=131072)
);
PRAGMA user_version=12;
`
