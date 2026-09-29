package store

import "github.com/delinoio/oss/cmds/delidev-cli/internal/domain"

func (t *Tx) SessionTerminals(id domain.ID) ([]Record, error) {
	records, err := t.List(Filter{Kind: domain.TerminalKind, SessionID: id, Limit: domain.MaxTerminalRecords + 1})
	if err != nil {
		return nil, err
	}
	if len(records) > domain.MaxTerminalRecords {
		return nil, domain.Fail(domain.ResourceExhausted, "The session terminal history limit is reached.", "Preserve the original terminal ownership records.")
	}
	return records, nil
}

// Every session publication shares this barrier, including late agent/title
// completion and recovery. None may mark Archive complete ahead of terminals.
func (t *Tx) terminalArchiveBarrier(id domain.ID, value any) (any, error) {
	session, ok := value.(domain.Session)
	if !ok || (session.Archive != domain.Archived && session.Archive != domain.ArchivePending) {
		return value, nil
	}
	records, err := t.SessionTerminals(id)
	if err != nil {
		return nil, err
	}
	for _, r := range records {
		terminal, err := Decode[domain.Terminal](r)
		if err != nil {
			return nil, err
		}
		if !terminal.Live() {
			continue
		}
		session.Archive = domain.ArchivePending
		if terminal.CloseRequestID == "" {
			terminal.CloseRequestID = domain.NewID()
			if _, err := t.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, terminal); err != nil {
				return nil, err
			}
		}
	}
	return session, nil
}

func (t *Tx) requireTerminalCleanup(id domain.ID) error {
	records, err := t.SessionTerminals(id)
	if err != nil {
		return err
	}
	for _, r := range records {
		terminal, err := Decode[domain.Terminal](r)
		if err != nil {
			return err
		}
		if terminal.Live() {
			return domain.Fail(domain.RecoveryRequired, "Session terminals have not confirmed cleanup.", "Archive the session and join all owned terminals before deletion.")
		}
	}
	return nil
}
