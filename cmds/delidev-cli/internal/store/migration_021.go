// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration021(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, providerActivationSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const providerActivationSchema = `
CREATE UNIQUE INDEX IF NOT EXISTS provider_preset_unique ON entities(json_extract(body,'$.preset_id'))
 WHERE kind='provider' AND json_type(body,'$.preset_id')='text' AND json_extract(body,'$.preset_id')<>'';
PRAGMA user_version=21;
`
