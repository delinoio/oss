// SPDX-License-Identifier: Apache-2.0
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
	pending, err := t.SessionTerminalsPending(id)
	if err != nil {
		return err
	}
	if pending {
		return domain.Fail(domain.RecoveryRequired, "Session terminals have not confirmed cleanup.", "Close and join all owned terminals before deletion.")
	}
	return nil
}

// Deletion and Archive share the original terminal close intent. Reapplying an
// external deletion obligation must not replace an accepted close identity.
func (t *Tx) StopTerminals(id domain.ID) error {
	records, err := t.SessionTerminals(id)
	if err != nil {
		return err
	}
	for _, r := range records {
		value, err := Decode[domain.Terminal](r)
		if err != nil {
			return err
		}
		if value.Live() && value.CloseRequestID == "" {
			value.CloseRequestID = domain.NewID()
			if _, err := t.Put(domain.TerminalKind, r.ID, r.Revision, r.SessionID, r.ProjectID, value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (t *Tx) SessionTerminalsPending(id domain.ID) (bool, error) {
	records, err := t.SessionTerminals(id)
	if err != nil {
		return false, err
	}
	for _, r := range records {
		terminal, err := Decode[domain.Terminal](r)
		if err != nil {
			return false, err
		}
		if terminal.Live() {
			return true, nil
		}
	}
	return false, nil
}
