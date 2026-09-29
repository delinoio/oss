package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) ActiveMachineTerminals(machine domain.ID) ([]Record, error) {
	rows, err := t.tx.QueryContext(t.ctx, "SELECT id,kind,revision,session_id,project_id,body,created_at,updated_at FROM entities WHERE kind=? AND json_extract(body,'$.machine_id')=? AND json_extract(body,'$.cleanup_verified')=0 ORDER BY id LIMIT 33", domain.TerminalKind, machine)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	var records []Record
	for rows.Next() {
		r, err := scan(rows)
		if err != nil {
			return nil, storageError(err)
		}
		records = append(records, r)
	}
	return records, storageError(rows.Err())
}

func (t *Tx) CompleteTerminalArchive(sessionID domain.ID) error {
	r, err := t.Get(domain.SessionKind, sessionID)
	if err != nil {
		return err
	}
	session, err := Decode[domain.Session](r)
	if err != nil {
		return err
	}
	if session.Archive != domain.ArchivePending || session.ActiveExecutionID != "" || session.Recovery != domain.NoRecovery {
		return nil
	}
	if session.Preparation != nil && (session.Preparation.State == domain.PreparationPending || session.Preparation.State == domain.PreparationStopping || session.Preparation.State == domain.PreparationUncertain) {
		return nil
	}
	if session.TitleJobID != "" {
		titleRecord, err := t.Get(domain.JobKind, session.TitleJobID)
		if err != nil {
			return err
		}
		job, err := Decode[domain.Job](titleRecord)
		if err != nil {
			return err
		}
		if job.State == domain.JobClaimed || job.State == domain.JobUncertain || job.State == domain.JobQueued {
			return nil
		}
	}
	if session.TitleState == domain.TitleRunning || session.TitleState == domain.TitleUncertain {
		return nil
	}
	if session.InitialExecution != nil && (session.Execution == nil || !session.Execution.CleanupVerified) && session.StartupRejection == nil {
		return nil
	}
	if err := t.requireTerminalCleanup(sessionID); err != nil {
		return nil
	}
	session.Archive = domain.Archived
	_, err = t.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	return err
}
