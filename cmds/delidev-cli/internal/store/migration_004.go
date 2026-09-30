// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration004(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, sessionSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const sessionSchema = `
CREATE INDEX session_visibility ON entities(project_id,json_extract(body,'$.archive'),id) WHERE kind='session';
CREATE UNIQUE INDEX queue_sequence ON entities(session_id,json_extract(body,'$.sequence')) WHERE kind='queue';
CREATE INDEX queue_pending ON entities(session_id,json_extract(body,'$.delivery'),json_extract(body,'$.sequence')) WHERE kind='queue';
PRAGMA user_version=4;
`
