// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Called under the terminal manager mutex and the Worker's exclusive root
// lifecycle lock. A confirmed terminal never starts another native operation;
// the generic process reconciler retains empty owner indexes until this point.
func (m *terminalManager) retireReported(j terminalOperationJournal) error {
	if j.Phase != terminalReported || j.TerminalID.Validate() != nil || j.OperationID.Validate() != nil || j.Result == nil || j.Result.Validate() != nil {
		return domain.TerminalUnavailable()
	}
	if j.Result.CleanupVerified {
		if _, live := m.live[j.TerminalID]; live {
			return domain.TerminalUnavailable()
		}
		owner := filepath.Join(m.processRoot(), string(j.TerminalID))
		if _, err := os.Lstat(owner); err == nil {
			// Reconcile once more to prune completed scopes after Handle.Close
			// released their controller locks. Never recursively erase live proof.
			if err := m.reconcile(j.TerminalID); err != nil {
				return err
			}
			if err := security.CheckPrivateDir(owner); err != nil {
				return err
			}
			if err := os.Remove(owner); err != nil { // Refuses nonempty indexes.
				return domain.TerminalUnavailable()
			}
			if err := security.SyncParent(owner); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		// No caller can recreate this terminal's owner after acknowledged cleanup.
		// Release the Windows lock handle before removing its private file.
		lockPath := owner + ".recovery.lock"
		lock, err := security.TryLockExisting(lockPath)
		if err == nil {
			if err := lock.Close(); err != nil {
				return err
			}
			if err := os.Remove(lockPath); err != nil {
				return err
			}
			if err := security.SyncParent(lockPath); err != nil {
				return err
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := removeTerminalMetadata(m.shutdownPath(j.TerminalID)); err != nil {
			return err
		}
	}
	return removeTerminalMetadata(m.journalPath(j.OperationID))
}

func removeTerminalMetadata(path string) error {
	if err := security.RegularPrivate(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.Remove(path); err != nil {
		return err
	}
	return security.SyncParent(path)
}
