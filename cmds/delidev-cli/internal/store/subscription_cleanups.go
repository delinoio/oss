// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Explicit batches own their pending cleanup and suppress automatic retries of
// an attempted retained result for that exact login. Restored canceled jobs do
// not participate. A fresh explicit batch uses the shared checkpoint directly.
func (t *Tx) FailedSubscriptionCleanupOwns(account, operation domain.ID) (bool, error) {
	var owns bool
	err := t.tx.QueryRowContext(t.ctx, `SELECT EXISTS(SELECT 1 FROM jobs j JOIN entities e ON e.id=j.id WHERE j.machine_id='' AND json_extract(e.body,'$.type')=? AND json_extract(e.body,'$.input.account_id')=? AND json_extract(e.body,'$.input.operation_id')=? AND (j.state IN ('queued','uncertain') OR (j.state='succeeded' AND json_extract(e.body,'$.output.outcome')=3 AND json_extract(e.body,'$.output.started')=1)))`, domain.CleanupFailedSubscriptionJob, account, operation).Scan(&owns)
	return owns, storageError(err)
}

// Filter before bounding: unrelated Worker history cannot hide an active batch.
func (t *Tx) FailedSubscriptionCleanupJobs(after domain.ID, limit int, pending bool) ([]Record, error) {
	if limit < 1 || limit > MaxPage || after != "" && after.Validate() != nil {
		return nil, domain.Fail(domain.InvalidArgument, "Invalid cleanup job page.", "Use the original bounded cursor.")
	}
	query := `SELECT e.id,e.kind,e.revision,e.session_id,e.project_id,e.body,e.created_at,e.updated_at FROM jobs j JOIN entities e ON e.id=j.id WHERE j.machine_id='' AND j.parent_id='' AND e.id>? AND json_extract(e.body,'$.type')=?`
	args := []any{after, domain.CleanupFailedSubscriptionsJob}
	if pending {
		query += ` AND j.state IN (?,?)`
		args = append(args, domain.JobQueued, domain.JobUncertain)
	}
	query += ` ORDER BY e.id LIMIT ?`
	args = append(args, limit)
	rows, err := t.tx.QueryContext(t.ctx, query, args...)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var result []Record
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		result = append(result, row)
	}
	return result, storageError(rows.Err())
}

func (s *Store) FailedSubscriptionCleanupJobs(ctx context.Context) ([]Record, error) {
	var rows []Record
	err := s.Read(ctx, func(tx *Tx) error {
		if err := tx.Authorize(); err != nil {
			return err
		}
		var err error
		rows, err = tx.FailedSubscriptionCleanupJobs("", 2, true)
		return err
	})
	return rows, err
}
