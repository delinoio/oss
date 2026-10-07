// SPDX-License-Identifier: Apache-2.0
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
	if session.Archive != domain.ArchivePending {
		return nil
	}
	if session.ActiveExecutionID != "" || session.Recovery != domain.NoRecovery || session.TitleState == domain.TitleUncertain || session.TitleState == domain.TitleRunning || session.InitialExecution != nil && (session.Execution == nil || !session.Execution.CleanupVerified) {
		domain.ObserveOwnership(domain.OwnershipCleanup, sessionID)
	}
	if err := t.requireTerminalCleanup(sessionID); err != nil {
		if domain.SafeError(err).Code == domain.RecoveryRequired {
			return nil
		}
		// A failed history lookup must roll back the report and its receipt so
		// the exact Worker report can retry Archive completion after repair.
		return err
	}
	session.Archive = domain.Archived
	_, err = t.Put(domain.SessionKind, r.ID, r.Revision, r.SessionID, r.ProjectID, session)
	return err
}
