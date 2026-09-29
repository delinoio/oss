package store

import (
	"context"
	"database/sql"
)

// Session titles are additive usage observations. Existing response rows are
// conversation usage by definition; the default preserves their attribution.
const sessionTitleSchema = `
CREATE INDEX IF NOT EXISTS response_usage_purpose_time ON response_usage(purpose,created_at,id);
CREATE TABLE IF NOT EXISTS session_title_send_claims (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 claimed_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS session_title_http_claims (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 claimed_at INTEGER NOT NULL
);
PRAGMA user_version=21;
`

func applySessionTitleSchema(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, "PRAGMA table_info(response_usage)")
	if err != nil {
		return storageError(err)
	}
	found := false
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, columnType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &columnType, &notNull, &defaultValue, &primaryKey); err != nil {
			rows.Close()
			return storageError(err)
		}
		found = found || name == "purpose"
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return storageError(err)
	}
	if err := rows.Close(); err != nil {
		return storageError(err)
	}
	if !found {
		if _, err := tx.ExecContext(ctx, `ALTER TABLE response_usage ADD COLUMN purpose TEXT NOT NULL DEFAULT 'conversation' CHECK(purpose IN ('conversation','session-title'))`); err != nil {
			return storageError(err)
		}
	}
	if _, err := tx.ExecContext(ctx, sessionTitleSchema); err != nil {
		return storageError(err)
	}
	return nil
}
