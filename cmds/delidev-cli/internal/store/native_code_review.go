// SPDX-License-Identifier: Apache-2.0
package store

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

const nativeReviewProgressPrefix = "native-code-review-progress-v1:"

func (t *Tx) NativeCodeReviewProgress(job domain.ID) (domain.NativeCodeReviewProgress, bool, error) {
	var value domain.NativeCodeReviewProgress
	if job.Validate() != nil {
		return value, false, domain.NativeCodeReviewUnavailable()
	}
	var raw string
	err := t.tx.QueryRowContext(t.ctx, "SELECT value FROM metadata WHERE key=?", nativeReviewProgressPrefix+string(job)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return value, false, nil
	}
	if err != nil {
		return value, false, storageError(err)
	}
	if len(raw) > domain.MaxPromptBytes+8192 || domain.Decode([]byte(raw), &value) != nil || value.Validate() != nil {
		return value, false, domain.NativeCodeReviewUnavailable()
	}
	return value, true, nil
}
func (t *Tx) PutNativeCodeReviewProgress(job domain.ID, value domain.NativeCodeReviewProgress) error {
	if err := t.writeAllowed(); err != nil {
		return err
	}
	if job.Validate() != nil || value.Validate() != nil {
		return domain.NativeCodeReviewUnavailable()
	}
	raw, err := json.Marshal(value)
	if err != nil || len(raw) > domain.MaxPromptBytes+8192 {
		return domain.NativeCodeReviewUnavailable()
	}
	_, err = t.tx.ExecContext(t.ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", nativeReviewProgressPrefix+string(job), string(raw))
	return storageError(err)
}

func (t *Tx) ActiveNativeCodeReview(session domain.ID) (Record, domain.Job, bool, error) {
	var row Record
	var job domain.Job
	var id domain.ID
	err := t.tx.QueryRowContext(t.ctx, `SELECT id FROM entities WHERE kind='job' AND session_id=? AND json_extract(body,'$.type')='native-code-review' AND json_extract(body,'$.state') IN ('queued','claimed','uncertain') ORDER BY id LIMIT 1`, session).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return row, job, false, nil
	}
	if err != nil {
		return row, job, false, storageError(err)
	}
	row, err = t.Get(domain.JobKind, id)
	if err != nil {
		return row, job, false, err
	}
	job, err = Decode[domain.Job](row)
	return row, job, true, err
}
func (t *Tx) RequireNoNativeCodeReview(session domain.ID) error {
	_, _, found, err := t.ActiveNativeCodeReview(session)
	if err != nil {
		return err
	}
	if found {
		return domain.Fail(domain.Conflict, "The original native code review is unfinished.", "Observe or stop that review before advancing its selected workspace; uncertainty never authorizes a replacement.")
	}
	return nil
}
