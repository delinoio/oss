// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration020(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, prRemediationSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const prRemediationSchema = `
CREATE TABLE pr_remediation_attempts (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 chain_id TEXT NOT NULL,
 sequence INTEGER NOT NULL CHECK(sequence BETWEEN 1 AND 10000),
 state TEXT NOT NULL CHECK(state IN ('reserved','bound','running','uncertain','finished','canceled')),
 input_id TEXT,
 UNIQUE(set_id,sequence),
 UNIQUE(input_id)
);
CREATE UNIQUE INDEX pr_remediation_active ON pr_remediation_attempts(set_id)
 WHERE state IN ('reserved','bound','running','uncertain');
CREATE INDEX pr_remediation_history ON pr_remediation_attempts(set_id,sequence);
PRAGMA user_version=20;
`
