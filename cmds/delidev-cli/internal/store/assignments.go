package store

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Keep the exact first claimed envelope for journal proof after the mutable
// job result changes. Deleting the job also deletes this retained copy.

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
