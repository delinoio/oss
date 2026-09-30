// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func migration018(ctx context.Context, tx *sql.Tx, original int) error {
	var legacyProblems int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE kind='problem'").Scan(&legacyProblems); err != nil {
		return storageError(err)
	}
	if legacyProblems != 0 {
		return domain.Fail(domain.RecoveryRequired, "Legacy PR problem ownership is unrecognized.", "Preserve the original database and migration backup; no old evidence is reinterpreted.")
	}
	if _, err := tx.ExecContext(ctx, prProblemSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const prProblemSchema = `
CREATE TABLE pr_problem_sets (
 id TEXT NOT NULL UNIQUE REFERENCES entities(id) ON DELETE CASCADE,
 stable_key TEXT PRIMARY KEY CHECK(length(stable_key)=64)
);
CREATE TABLE pr_problem_records (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 kind TEXT NOT NULL CHECK(kind='review-feedback'),
 native_node TEXT NOT NULL,
 content_version TEXT NOT NULL CHECK(length(content_version)=64),
 current INTEGER NOT NULL CHECK(current IN (0,1)),
 UNIQUE(set_id,kind,native_node,content_version)
);
CREATE INDEX pr_problem_current ON pr_problem_records(set_id,kind,current,id);
CREATE INDEX pr_problem_history ON pr_problem_records(set_id,id);
PRAGMA user_version=18;
`
