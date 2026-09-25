package store

import (
	"bytes"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Keep the exact first claimed envelope for journal proof after the mutable
// job result changes. Deleting the job also deletes this retained copy.
const assignmentSchema = `
CREATE TABLE job_assignments (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 revision INTEGER NOT NULL, session_id TEXT NOT NULL, project_id TEXT NOT NULL,
 body BLOB NOT NULL, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL
);
PRAGMA user_version=6;
`

func (t *Tx) rememberAssignment(record Record) error {
	_, err := t.tx.ExecContext(t.ctx, "INSERT INTO job_assignments(job_id,revision,session_id,project_id,body,created_at,updated_at) VALUES(?,?,?,?,?,?,?) ON CONFLICT(job_id) DO NOTHING", record.ID, record.Revision, record.SessionID, record.ProjectID, record.Data, record.CreatedAt.UnixMilli(), record.UpdatedAt.UnixMilli())
	return storageError(err)
}

func (t *Tx) JobAssignment(id domain.ID) (Record, error) {
	if err := id.Validate(); err != nil {
		return Record{}, err
	}
	result, err := scan(t.tx.QueryRowContext(t.ctx, "SELECT job_id,'job',revision,session_id,project_id,body,created_at,updated_at FROM job_assignments WHERE job_id=?", id))
	if err != nil {
		return Record{}, domain.Fail(domain.RecoveryRequired, "The original Worker assignment is unavailable.", "Preserve the job and Worker journal; restore the matching pre-change assignment before recovery.")
	}
	if result.ID != id || result.Kind != domain.JobKind || result.Revision == 0 {
		return Record{}, domain.Fail(domain.RecoveryRequired, "The retained Worker assignment is inconsistent.", "Preserve the data scope for recovery.")
	}
	return result, nil
}

func (t *Tx) preserveLegacyAssignments() error {
	for _, state := range []domain.JobState{domain.JobClaimed, domain.JobUncertain} {
		var after domain.ID
		for {
			records, err := t.Jobs("", "", state, after, MaxPage)
			if err != nil {
				return err
			}
			for _, record := range records {
				if state == domain.JobClaimed {
					if err := t.rememberAssignment(record); err != nil {
						return err
					}
				} else if err := t.restoreReceiptAssignment(record); err != nil {
					return err
				}
				after = record.ID
			}
			if len(records) < MaxPage {
				break
			}
		}
	}
	return nil
}

// Older schemas already retained claim receipts. Use an exact original record
// when available; never invent a claimed envelope from a changed outcome.
func (t *Tx) restoreReceiptAssignment(record Record) error {
	original, err := Decode[domain.Job](record)
	if err != nil {
		return err
	}
	rows, err := t.tx.QueryContext(t.ctx, `SELECT r.result FROM receipts r JOIN receipt_entities e ON e.request_id=r.id
 WHERE e.entity_id=? AND json_extract(r.result,'$.kind')='job' AND json_extract(r.result,'$.id')=?
 AND json_extract(r.result,'$.data.state')='claimed' LIMIT 201`, record.ID, record.ID)
	if err != nil {
		return storageError(err)
	}
	defer rows.Close()
	var candidate *Record
	count := 0
	for rows.Next() {
		count++
		if count > 200 {
			return domain.Fail(domain.RecoveryRequired, "Legacy assignment evidence exceeds its recovery bound.", "Preserve the original database and inspect duplicate claim receipts.")
		}
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			return storageError(err)
		}
		var r Record
		if err := domain.Decode(raw, &r); err != nil {
			return err
		}
		claim, err := Decode[domain.Job](r)
		if err != nil {
			return err
		}
		if r.ID != record.ID || r.Kind != domain.JobKind || r.SessionID != record.SessionID || r.ProjectID != record.ProjectID || r.Revision >= record.Revision || claim.Type != original.Type || claim.InstanceID != original.InstanceID || claim.MachineID != original.MachineID || claim.ParentID != original.ParentID || !claim.AcceptedAt.Equal(original.AcceptedAt) || !bytes.Equal(claim.Input, original.Input) {
			return domain.Fail(domain.RecoveryRequired, "Legacy claim receipts conflict with accepted job ownership.", "Preserve the original database and Worker journal.")
		}
		if candidate != nil && (candidate.Revision != r.Revision || !bytes.Equal(candidate.Data, r.Data)) {
			return domain.Fail(domain.RecoveryRequired, "Legacy claim receipts disagree.", "Preserve both records instead of choosing an assignment.")
		}
		candidate = &r
	}
	if err := rows.Err(); err != nil {
		return storageError(err)
	}
	if err := rows.Close(); err != nil {
		return storageError(err)
	}
	if candidate != nil {
		return t.rememberAssignment(*candidate)
	}
	return nil
}
