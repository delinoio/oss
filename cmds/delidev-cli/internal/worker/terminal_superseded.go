// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

type terminalSupersededRecord struct {
	TerminalID  domain.ID `json:"terminal_id"`
	OperationID domain.ID `json:"operation_id"`
}

func (m *terminalManager) supersededPath(id domain.ID) string {
	return filepath.Join(m.config.Root, "terminal-superseded", string(id)+".json")
}

// One terminal has at most one pending operation when its close wins. Keep
// this metadata through uncertain close reports, which clear server Pending.
// Neither a failed claim nor this record alone authorizes native work or removal.
func (m *terminalManager) saveSuperseded(id, operation domain.ID) error {
	if id.Validate() != nil || operation.Validate() != nil {
		return domain.TerminalUnavailable()
	}
	if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-superseded")); err != nil {
		return err
	}
	value := terminalSupersededRecord{id, operation}
	raw, err := security.ReadPrivate(m.supersededPath(id), 4096)
	if err == nil {
		var prior terminalSupersededRecord
		if domain.Decode(raw, &prior) != nil || prior != value {
			return domain.TerminalUnavailable()
		}
		return nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err = json.Marshal(value)
	if err != nil {
		return err
	}
	return security.WriteAtomic(m.supersededPath(id), raw)
}

// Called only after the cleanup report's acknowledgement is synchronized.
// Prepared proves no native intent was recorded locally; acknowledged cleanup
// independently closes any server claim whose reply was lost. Direct UUID
// lookup bounds this work without scanning or deleting unrelated journals.
func (m *terminalManager) retireSuperseded(id domain.ID) error {
	raw, err := security.ReadPrivate(m.supersededPath(id), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	var ref terminalSupersededRecord
	if err != nil || domain.Decode(raw, &ref) != nil || ref.TerminalID != id || ref.OperationID.Validate() != nil {
		return domain.TerminalUnavailable()
	}
	path := m.journalPath(ref.OperationID)
	raw, err = security.ReadPrivate(path, terminalOperationJournalMaxBytes)
	if !errors.Is(err, os.ErrNotExist) {
		var pending terminalOperationJournal
		if err != nil || domain.Decode(raw, &pending) != nil || pending.TerminalID != id || pending.OperationID != ref.OperationID || pending.InstanceID.Validate() != nil || pending.ClaimID.Validate() != nil || pending.ReportID.Validate() != nil {
			return domain.TerminalUnavailable()
		}
		if pending.Phase == terminalPrepared && pending.Result == nil && pending.CloseRecovery == nil {
			digest, err := hex.DecodeString(pending.Digest)
			if err != nil || len(digest) != 32 || hex.EncodeToString(digest) != pending.Digest {
				return domain.TerminalUnavailable()
			}
			if err := removeTerminalMetadata(path); err != nil {
				return err
			}
			m.config.Logger.Info("terminal_superseded_journal_retired", "terminal_id", id, "operation_id", ref.OperationID)
		}
		// Claimed/started/finished evidence has its own recovery/receipt lifetime.
	}
	return removeTerminalMetadata(m.supersededPath(id))
}
