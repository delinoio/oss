package store

import (
	"database/sql"
	"errors"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Rebuild only the typed index to widen its closed kind set. Original resource
// bytes, revisions, receipts, events and local handling are never rewritten.
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

func (t *Tx) GetPRCIObservation(id domain.ID) (Record, domain.PRCIObservation, error) {
	if _, err := t.prProblemActor(); err != nil {
		return Record{}, domain.PRCIObservation{}, err
	}
	row, err := t.Get(domain.ProblemKind, id)
	if err != nil {
		return row, domain.PRCIObservation{}, err
	}
	value, err := Decode[domain.PRCIObservation](row)
	if err != nil {
		return row, value, err
	}
	if value.Validate() != nil || row.SessionID != "" || row.ProjectID != "" {
		return row, value, prProblemConflict()
	}
	var set domain.ID
	var digest string
	if err = t.tx.QueryRowContext(t.ctx, "SELECT set_id,digest FROM pr_problem_ci_observations WHERE id=?", id).Scan(&set, &digest); err != nil {
		return row, value, storageError(err)
	}
	if set != value.SetID || digest != value.Digest() {
		return row, value, prProblemConflict()
	}
	_, parent, err := t.GetPRProblemSet(set)
	if err != nil {
		return row, value, err
	}
	if !parent.Target.SamePR(value.Target) || parent.Target.RepositoryNodeID != value.Target.RepositoryNodeID || parent.Target.PullRequestNodeID != value.Target.PullRequestNodeID {
		return row, value, prProblemConflict()
	}
	return row, value, nil
}

func (t *Tx) putPRCIObservation(value domain.PRCIObservation) (Record, error) {
	if err := value.Validate(); err != nil {
		return Record{}, err
	}
	digest := value.Digest()
	var existing domain.ID
	err := t.tx.QueryRowContext(t.ctx, "SELECT id FROM pr_problem_ci_observations WHERE set_id=? AND digest=?", value.SetID, digest).Scan(&existing)
	if err == nil {
		row, _, err := t.GetPRCIObservation(existing)
		return row, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return Record{}, storageError(err)
	}
	row, err := t.Put(domain.ProblemKind, domain.NewID(), 0, "", "", value)
	if err != nil {
		return row, err
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO pr_problem_ci_observations(id,set_id,digest) VALUES(?,?,?)", row.ID, value.SetID, digest)
	return row, storageError(err)
}
