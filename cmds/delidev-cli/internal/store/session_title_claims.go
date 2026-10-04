package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

// ClaimTitleInference is the durable once-only send boundary. Registration is
// committed before the Worker can open the native runtime or send its prompt.
func (t *Tx) ClaimTitleInference(jobID domain.ID) (bool, error) {
	if err := t.writeAllowed(); err != nil {
		return false, err
	}
	record, err := t.Get(domain.JobKind, jobID)
	if err != nil {
		return false, err
	}
	job, err := Decode[domain.Job](record)
	if err != nil || job.Type != domain.GenerateSessionTitleJob || job.State != domain.JobClaimed {
		return false, domain.Fail(domain.PermissionDenied, "Title inference requires its original claimed auxiliary assignment.", "Keep native send authority bound to one title job.")
	}
	result, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO session_title_send_claims(job_id,claimed_at) VALUES(?,?)", jobID, t.now.UnixMilli())
	if err != nil {
		return false, storageError(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, storageError(err)
	}
	return changed == 1, nil
}

func (t *Tx) TitleInferenceClaimed(jobID domain.ID) (bool, error) {
	if err := jobID.Validate(); err != nil {
		return false, err
	}
	var claimed bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM session_title_send_claims WHERE job_id=?)", jobID).Scan(&claimed)
	return claimed, storageError(err)
}

func (t *Tx) TitleHTTPRequestClaimed(jobID domain.ID) (bool, error) {
	if err := jobID.Validate(); err != nil {
		return false, err
	}
	var claimed bool
	err := t.tx.QueryRowContext(t.ctx, "SELECT EXISTS(SELECT 1 FROM session_title_http_claims WHERE job_id=?)", jobID).Scan(&claimed)
	return claimed, storageError(err)
}

// ClaimTitleHTTPRequest reserves the original upstream-send boundary. Its
// transaction also retains send metadata; neither proves provider acceptance.
func (t *Tx) ClaimTitleHTTPRequest(jobID domain.ID) (bool, error) {
	if err := t.writeAllowed(); err != nil {
		return false, err
	}
	if _, err := t.Get(domain.JobKind, jobID); err != nil {
		return false, err
	}
	result, err := t.tx.ExecContext(t.ctx, "INSERT OR IGNORE INTO session_title_http_claims(job_id,claimed_at) VALUES(?,?)", jobID, t.now.UnixMilli())
	if err != nil {
		return false, storageError(err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, storageError(err)
	}
	return changed == 1, nil
}
