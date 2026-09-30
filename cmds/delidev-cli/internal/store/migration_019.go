// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
)

func migration019(ctx context.Context, tx *sql.Tx, original int) error {
	if _, err := tx.ExecContext(ctx, prCIProblemSchema); err != nil {
		return storageError(err)
	}
	return nil
}

const prCIProblemSchema = `
ALTER TABLE pr_problem_records RENAME TO pr_problem_records_v18;
DROP INDEX pr_problem_current;
DROP INDEX pr_problem_history;
CREATE TABLE pr_problem_ci_observations (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 digest TEXT NOT NULL CHECK(length(digest)=64),
 UNIQUE(set_id,digest)
);
CREATE TABLE pr_problem_records (
 id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 set_id TEXT NOT NULL REFERENCES pr_problem_sets(id),
 kind TEXT NOT NULL CHECK(kind IN ('review-feedback','ci-failure','merge-conflict')),
 native_node TEXT NOT NULL,
 content_version TEXT NOT NULL CHECK(length(content_version)=64),
 current INTEGER NOT NULL CHECK(current IN (0,1)),
 ci_observation_id TEXT REFERENCES pr_problem_ci_observations(id),
 CHECK((kind='ci-failure' AND ci_observation_id IS NOT NULL) OR (kind!='ci-failure' AND ci_observation_id IS NULL)),
 UNIQUE(set_id,kind,native_node,content_version)
);
INSERT INTO pr_problem_records(id,set_id,kind,native_node,content_version,current)
 SELECT id,set_id,kind,native_node,content_version,current FROM pr_problem_records_v18;
DROP TABLE pr_problem_records_v18;
CREATE INDEX pr_problem_current ON pr_problem_records(set_id,kind,current,id);
CREATE INDEX pr_problem_history ON pr_problem_records(set_id,id);
PRAGMA user_version=19;
`
