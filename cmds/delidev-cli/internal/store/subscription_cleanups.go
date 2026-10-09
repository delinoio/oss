// SPDX-License-Identifier: Apache-2.0
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Single-account cleanup reserves the public deletion command before its final
// receipt exists. Use the original revision from its immutable child input,
// never the cleanup checkpoint revision. Parent lookup uses the existing index;
// ordinary batches have no delete_revision and retain their original behavior.
func (t *Tx) checkSubscriptionDeletionRequest(id domain.ID, digest string) error {
	rows, err := t.tx.QueryContext(t.ctx, `SELECT e.body FROM jobs j JOIN entities e ON e.id=j.id WHERE j.parent_id=? AND j.machine_id='' AND json_extract(e.body,'$.type')=? AND json_extract(e.body,'$.input.delete_revision') IS NOT NULL LIMIT 2`, id, domain.CleanupFailedSubscriptionJob)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		count++
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return storageError(err)
		}
		var job domain.Job
		var input struct {
			Version         uint32    `json:"version"`
			AccountID       domain.ID `json:"account_id"`
			DeleteRequestID domain.ID `json:"delete_request_id"`
			DeleteRevision  uint64    `json:"delete_revision,string"`
		}
		if count != 1 || json.Unmarshal(raw, &job) != nil || json.Unmarshal(job.Input, &input) != nil || input.Version != 2 || input.AccountID.Validate() != nil || input.DeleteRequestID != id || input.DeleteRevision == 0 {
			return domain.Fail(domain.RecoveryRequired, "The original deletion request reservation is unavailable.", "Inspect its original cleanup job without reusing the request ID.")
		}
		original, err := mutationDigest(id, "configuration.delete", struct {
			ID       string      `json:"id"`
			Revision uint64      `json:"revision"`
			Kind     domain.Kind `json:"kind"`
		}{string(input.AccountID), input.DeleteRevision, domain.AccountKind})
		if err != nil {
			return err
		}
		if original != digest {
			return domain.Fail(domain.Conflict, "The request ID is reserved for an original account deletion.", "Retry the original deletion unchanged or use a new request ID.")
		}
	}
	return storageError(rows.Err())
}

// Admission checks the public identity inside the same transaction that inserts
// its cleanup children. A different command cannot win between preflight and
// durable reservation, even when that command does not use the account gate.
func (t *Tx) RequireUnusedSubscriptionDeletionRequest(id domain.ID) error {
	if err := id.Validate(); err != nil {
		return err
	}
	var present int
	err := t.tx.QueryRowContext(t.ctx, "SELECT 1 FROM receipts WHERE id=?", id).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return storageError(err)
	}
	return domain.Fail(domain.Conflict, "The deletion request ID was already used.", "Use a fresh explicit deletion confirmation.")
}

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
