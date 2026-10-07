// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func (m *Manager) readProcessRoot(session domain.ID) string {
	return filepath.Join(m.Root, "workspace-read-processes-v2", string(session))
}

// Execution keeps its separate lifetime lock so views still work during a run.
// This cross-process gate fences destructive storage through anchored reads and
// independent read-child cleanup. Busy admission creates no workspace effects.
func (m *Manager) lockWorkspaceObservations(ctx context.Context, session domain.ID) (*security.Lock, error) {
	lock, err := security.TryLock(filepath.Join(m.Root, "locks", string(session)+".workspace-read.lock"))
	if err != nil {
		if domain.SafeError(err).Code == domain.Conflict {
			m.Logger.Debug("workspace_observation_gate_busy", "session_id", session)
			return nil, domain.Fail(domain.Conflict, "A workspace observation or storage operation still owns this session.", "Wait for its original cleanup before requesting another observation or storage operation.")
		}
		return nil, err
	}
	if err := m.quiesceWorkspaceReads(ctx, session); err != nil {
		lock.Close()
		m.Logger.Warn("workspace_observation_cleanup_pending", "session_id", session, "code", domain.SafeError(err).Code)
		return nil, ResultUncertain()
	}
	return lock, nil
}

// Version 1 had unassigned flat read indexes. Never infer their session from
// current paths or delete them as another session's cleanup. Retained legacy
// ownership requires recovery; new indexes bind a session before child launch.
func (m *Manager) quiesceWorkspaceReads(ctx context.Context, session domain.ID) error {
	legacy := filepath.Join(m.Root, "workspace-read-processes")
	if names, err := boundedReadNames(legacy); err == nil {
		if len(names) != 0 {
			domain.ObserveOwnership(domain.OwnershipCleanup, session)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	root := m.readProcessRoot(session)
	if err := security.CheckPrivateDir(filepath.Dir(root)); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return ResultUncertain()
	}
	names, err := boundedReadNames(root)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return ResultUncertain()
	}
	owners := map[domain.ID]bool{}
	for _, name := range names {
		id := domain.ID(strings.TrimSuffix(name, ".recovery.lock"))
		if id.Validate() != nil {
			return ResultUncertain()
		}
		owners[id] = true
	}
	for id := range owners {
		if err := ctx.Err(); err != nil {
			return err
		}
		owner := filepath.Join(root, string(id))
		confirmed, err := process.ObserveOwnerContext(ctx, root, id)
		if err != nil {
			return err
		}
		if !confirmed {
			continue
		}
		maintenance := owner + ".recovery.lock"
		if security.RegularPrivate(maintenance) != nil || os.Remove(maintenance) != nil {
			return ResultUncertain()
		}
		// Retire the maintenance lock before its owner index. If a Worker exits
		// between these removals, the next reconciliation still has the owner
		// directory required by ReconcileOwnerContext; removing the owner first
		// would strand an unrecoverable lock-only record.
		if os.Remove(owner) != nil {
			return ResultUncertain()
		}
	}
	if names, err := boundedReadNames(root); err != nil {
		return err
	} else if len(names) != 0 {
		return nil
	}
	if os.Remove(root) != nil || security.SyncParent(root) != nil {
		return ResultUncertain()
	}
	return nil
}

func boundedReadNames(root string) ([]string, error) {
	if err := security.CheckPrivateDir(root); err != nil {
		return nil, err
	}
	file, err := os.Open(root)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	names, err := file.Readdirnames(1025)
	if err != nil && err != io.EOF || len(names) > 1024 {
		return nil, ResultUncertain()
	}
	return names, nil
}
