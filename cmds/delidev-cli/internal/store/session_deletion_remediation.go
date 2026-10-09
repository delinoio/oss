// SPDX-License-Identifier: Apache-2.0
package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// PR chains are shared across replacement sessions. After confirmed native
// cleanup, remove session operands from history without refunding lifetime
// attempts or breaking its contiguous sequence. Cancellation here retires only
// the coordination record; it does not manufacture a native Stop outcome.
func (t *Tx) redactSessionRemediation(session domain.ID) error {
	rows, e := t.tx.QueryContext(t.ctx, "SELECT id FROM entities WHERE kind='problem' AND json_extract(body,'$.type')=? AND json_extract(body,'$.session_id')=? LIMIT ?", domain.PRRemediationAttemptRecord, session, domain.MaxPRRemediationAttempts+1)
	if e != nil {
		return storageError(e)
	}
	ids := []domain.ID{}
	for rows.Next() {
		var id domain.ID
		if e := rows.Scan(&id); e != nil {
			rows.Close()
			return storageError(e)
		}
		ids = append(ids, id)
	}
	e = rows.Err()
	rows.Close()
	if e != nil {
		return storageError(e)
	}
	if len(ids) > domain.MaxPRRemediationAttempts {
		return domain.SessionDeletionPending()
	}
	for _, id := range ids {
		r, v, e := t.GetPRRemediationAttempt(id)
		if e != nil {
			return e
		}
		if e := t.purgeSessionAttemptActivity(id); e != nil {
			return e
		}
		if v.State.Active() {
			sr, set, e := t.GetPRProblemSet(v.SetID)
			if e != nil {
				return e
			}
			if set.Remediation == nil || set.Remediation.ActiveAttemptID != id {
				return domain.SessionDeletionPending()
			}
			set.Remediation.ActiveAttemptID = ""
			if _, e := t.publishPRProblemSet(sr, set); e != nil {
				return e
			}
		}
		v.SessionID, v.InputID, v.InputDigest, v.ExecutionID, v.StartupRejectionJobID = "", "", "", "", ""
		v.StartedAt, v.FinishedAt, v.Outcome, v.State = nil, &t.now, "", domain.PRRemediationCanceled
		if e := v.Validate(); e != nil {
			return e
		}
		// Erasure retires shared coordination metadata, not a remediation
		// action. Preserve the exact original erasure publication and receipt
		// redaction independently of retired Activity snapshot writers.
		if _, e := t.Put(domain.ProblemKind, id, r.Revision, "", "", v); e != nil {
			return e
		}
		if _, e := t.tx.ExecContext(t.ctx, "UPDATE pr_remediation_attempts SET state=?,input_id=NULL WHERE id=?", v.State, id); e != nil {
			return storageError(e)
		}
		if _, e := t.tx.ExecContext(t.ctx, "UPDATE receipts SET result=? WHERE id IN (SELECT request_id FROM receipt_entities WHERE entity_id=?)", []byte(`{"deleted":true}`), id); e != nil {
			return storageError(e)
		}
	}
	return nil
}

// Reservation activity predates session binding and has no session_id on its
// row. Its immutable source reference still owns it through the original
// attempt. Purge those copies before removing that attempt's session operands.
func (t *Tx) purgeSessionAttemptActivity(attempt domain.ID) error {
	for {
		rows, e := t.tx.QueryContext(t.ctx, "SELECT id,kind,revision,session_id,project_id,X'',created_at,updated_at FROM entities WHERE kind='problem' AND json_extract(body,'$.type')='pull-request-activity' AND json_extract(body,'$.source_id')=? ORDER BY id LIMIT 200", attempt)
		if e != nil {
			return storageError(e)
		}
		records := []Record{}
		for rows.Next() {
			r, e := scan(rows)
			if e != nil {
				rows.Close()
				return storageError(e)
			}
			records = append(records, r)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return storageError(e)
		}
		for _, r := range records {
			if e := t.deleteSessionRecord(r); e != nil {
				return e
			}
		}
		if len(records) < 200 {
			return nil
		}
	}
}
