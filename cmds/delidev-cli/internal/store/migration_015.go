// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration015(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, pricingSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const pricingSchema = `
CREATE TABLE pricing_versions (
 id TEXT PRIMARY KEY,
 model_id TEXT NOT NULL,
 provider_id TEXT NOT NULL,
 revision INTEGER NOT NULL CHECK(revision>0),
 body BLOB NOT NULL CHECK(length(body)<=16384),
 created_at INTEGER NOT NULL,
 UNIQUE(model_id,revision)
);
CREATE TABLE active_pricing (
 model_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 pricing_id TEXT NOT NULL UNIQUE REFERENCES pricing_versions(id)
);
CREATE TABLE response_estimates (
 usage_id TEXT PRIMARY KEY REFERENCES response_usage(id) ON DELETE CASCADE,
 pricing_id TEXT REFERENCES pricing_versions(id),
 body BLOB NOT NULL CHECK(length(body)<=16384)
);
INSERT INTO response_estimates(usage_id,body)
 SELECT id,'{"known_amount":"","coverage":"unavailable","input":{"state":"missing-price","tokens":null,"amount":""},"cached_input":{"state":"missing-price","tokens":null,"amount":""},"output":{"state":"missing-price","tokens":null,"amount":""}}' FROM response_usage;
PRAGMA user_version=15;
`
