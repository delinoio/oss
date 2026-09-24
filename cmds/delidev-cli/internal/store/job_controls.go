package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// Cancellation is separate from the immutable claimed job envelope. A control
// must not change the assignment revision/digest used by Worker crash journals.
const jobControlSchema = `
CREATE TABLE job_cancellations (
 job_id TEXT PRIMARY KEY REFERENCES entities(id) ON DELETE CASCADE,
 requested_at INTEGER NOT NULL
);
PRAGMA user_version=5;
`

func (t *Tx) RequestJobCancellation(id domain.ID) error {
	if t.readOnly {
		return domain.Fail(domain.PermissionDenied, "Read transactions cannot cancel work.", "Use the owning product operation.")
	}
	if _, err := t.Get(domain.JobKind, id); err != nil {
		return err
	}
	_, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO job_cancellations(job_id,requested_at) VALUES(?,?)", id, t.now.UnixMilli())
	t.touched[id] = true
	return storageError(err)
}

func (t *Tx) JobCancellationRequested(id domain.ID) (bool, error) {
	if err := id.Validate(); err != nil {
		return false, err
	}
	var found bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM job_cancellations WHERE job_id=?)", id).Scan(&found)
	return found, storageError(err)
}
