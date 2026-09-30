// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/terminal"
)

// Shutdown observations grant no report or native authority to a replacement.
// Only its separately claimed close may carry the retained loss observation;
// process cleanup must still be independently reconciled under the old owner.
type terminalShutdownRecord struct {
	TerminalID      domain.ID       `json:"terminal_id"`
	OwnerInstanceID domain.ID       `json:"owner_instance_id"`
	Result          terminal.Result `json:"result"`
}

func (m *terminalManager) shutdownPath(id domain.ID) string {
	return filepath.Join(m.config.Root, "terminal-shutdown", string(id)+".json")
}

func (m *terminalManager) saveShutdown(id domain.ID, result terminal.Result) error {
	if id.Validate() != nil || m.instance.Validate() != nil || result.Validate() != nil {
		return domain.TerminalUnavailable()
	}
	if err := security.PrivateDir(filepath.Join(m.config.Root, "terminal-shutdown")); err != nil {
		return err
	}
	raw, err := json.Marshal(terminalShutdownRecord{id, m.instance, result})
	if err != nil || len(raw) > terminalOperationJournalMaxBytes {
		return domain.TerminalUnavailable()
	}
	return security.WriteAtomic(m.shutdownPath(id), raw)
}

func (m *terminalManager) shutdownLoss(a terminal.Assignment) (bool, error) {
	raw, err := security.ReadPrivate(m.shutdownPath(a.ID), terminalOperationJournalMaxBytes)
	if errors.Is(err, os.ErrNotExist) {
		// Abrupt termination can abandon an unacknowledged output suffix before
		// shutdown writes any observation. Only positive original pre-native or
		// joined creation evidence can establish that no bytes were lost.
		if pending := a.Terminal.Pending; pending != nil && pending.Action == domain.TerminalCreate {
			if !pending.Claimed {
				return false, nil
			}
			raw, err := security.ReadPrivate(m.journalPath(pending.ID), terminalOperationJournalMaxBytes)
			var create terminalOperationJournal
			if err == nil && domain.Decode(raw, &create) == nil && create.TerminalID == a.ID && create.OperationID == pending.ID && create.InstanceID == a.Terminal.OwnerInstanceID && create.InstanceID.Validate() == nil {
				if create.Phase == terminalPrepared || create.Phase == terminalClaimed {
					return false, nil
				}
				if create.Phase == terminalFinished && create.Result != nil && create.Result.Validate() == nil && create.Result.CleanupVerified {
					return create.Result.OutputLost, nil
				}
			}
		}
		return true, nil
	}
	var record terminalShutdownRecord
	if err != nil || domain.Decode(raw, &record) != nil || record.TerminalID != a.ID || record.OwnerInstanceID != a.Terminal.OwnerInstanceID || record.OwnerInstanceID.Validate() != nil || record.Result.Validate() != nil || (record.Result.State != domain.TerminalExited && record.Result.State != domain.TerminalUncertain) {
		return false, domain.Fail(domain.RecoveryRequired, "The original terminal shutdown observation is unavailable or changed.", "Restore the original ownership evidence before cleanup.")
	}
	return record.Result.OutputLost, nil
}
