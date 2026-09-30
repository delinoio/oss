// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration014(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, responseUsageSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const responseUsageSchema = `
CREATE TABLE response_usage (
 id TEXT PRIMARY KEY,
 session_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,
 project_id TEXT NOT NULL,
 execution_id TEXT NOT NULL,
 account_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 model_id TEXT NOT NULL,
 response_digest TEXT NOT NULL CHECK(length(response_digest)=64),
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(account_id,provider_id,response_digest)
);
CREATE INDEX response_usage_time ON response_usage(created_at,id);
CREATE INDEX response_usage_session ON response_usage(session_id,created_at,id);
CREATE INDEX response_usage_project ON response_usage(project_id,created_at,id);
PRAGMA user_version=14;
`
