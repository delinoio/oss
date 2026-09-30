// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func migration010(ctx context.Context, tx *sql.Tx, original int) error {
	var legacyInbox int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE kind='inbox'").Scan(&legacyInbox); err != nil {
		return storageError(err)
	}
	if legacyInbox != 0 {
		return domain.Fail(domain.RecoveryRequired, "Legacy state contains unrecognized inbox ownership.", "Preserve the database and backup; do not replace or merge unvalidated inbox entries.")
	}
	if _, err := tx.ExecContext(ctx, inboxSchema); err != nil {
		return storageError(err)
	}
	backfill := &Tx{tx: tx, ctx: ctx, now: time.Now().UTC().Truncate(time.Millisecond), touched: map[domain.ID]bool{}}
	if err := backfill.preserveLegacyInbox(); err != nil {
		return err
	}
	return nil
}

const inboxSchema = `
CREATE UNIQUE INDEX inbox_source ON entities(json_extract(body,'$.source'),json_extract(body,'$.source_id')) WHERE kind='inbox';
CREATE INDEX inbox_read ON entities(json_extract(body,'$.read_state'),id) WHERE kind='inbox';
CREATE INDEX inbox_session_read ON entities(session_id,json_extract(body,'$.read_state'),id) WHERE kind='inbox';
PRAGMA user_version=10;
`
