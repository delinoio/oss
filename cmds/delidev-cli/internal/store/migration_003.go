// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration003(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, catalogSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const catalogSchema = `
CREATE UNIQUE INDEX model_canonical ON entities(json_extract(body,'$.provider_id'),json_extract(body,'$.native_id')) WHERE kind='model';
CREATE UNIQUE INDEX model_alias ON entities(json_extract(body,'$.alias')) WHERE kind='model' AND COALESCE(json_extract(body,'$.alias'),'')<>'';
CREATE TABLE model_suppressions(provider_id TEXT NOT NULL REFERENCES entities(id) ON DELETE CASCADE,native_id TEXT NOT NULL,PRIMARY KEY(provider_id,native_id));
CREATE INDEX model_native ON entities(json_extract(body,'$.native_id')) WHERE kind='model';
CREATE INDEX model_display ON entities(json_extract(body,'$.provider_id'),COALESCE(json_extract(body,'$.order'),0),lower(json_extract(body,'$.name')),id) WHERE kind='model';
CREATE INDEX event_kind_cursor ON events(kind,sequence);
PRAGMA user_version=3;
`
