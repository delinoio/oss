// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Cleanup Git children never recreate an originally absent session copy. Their
// independent process index lives under the original deletion obligation.
func deletionGitRoot(root string, w domain.SessionDeletionWork) (string, error) {
	paths, err := SessionDeletionProofPaths(root, w)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(paths.Workspace, ".json") + "-processes", nil
}
func RequireDeletionGitRemoved(root string, w domain.SessionDeletionWork) error {
	dir, err := deletionGitRoot(root, w)
	if err != nil {
		return err
	}
	for _, p := range []string{filepath.Join(dir, string(w.SessionID)), filepath.Join(dir, string(w.SessionID)+".recovery.lock")} {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
func reconcileDeletionGit(ctx context.Context, root string, w domain.SessionDeletionWork) error {
	dir, err := deletionGitRoot(root, w)
	if err != nil {
		return err
	}
	owner := filepath.Join(dir, string(w.SessionID))
	proofPath := filepath.Join(dir, "removal.json")
	proof := deletionWorkspaceProof{Version: 2, Identity: w.Identity(), Digest: w.Digest()}
	a, err := loadWorkspaceRemovalAdmission(proofPath, w, proof)
	if err != nil {
		return err
	}
	_, ownerErr := os.Lstat(owner)
	if errors.Is(ownerErr, os.ErrNotExist) && a == nil {
		return RequireDeletionGitRemoved(root, w)
	}
	if ownerErr != nil && !errors.Is(ownerErr, os.ErrNotExist) {
		return domain.SessionDeletionPending()
	}
	if a == nil {
		// Pin the owner tree before independent process-journal admission. The
		// reconciliation lock is established before capturing its removal operand.
		pin, err := security.CaptureRemovalAdmission(ctx, dir, []string{owner})
		if err != nil {
			return err
		}
		maintenance, err := security.TryLock(owner + ".recovery.lock")
		if err != nil {
			return domain.SessionDeletionPending()
		}
		if err := maintenance.Close(); err != nil {
			return domain.SessionDeletionPending()
		}
		if pin.Check(ctx, dir, false) != nil {
			return domain.SessionDeletionPending()
		}
		a, err = security.CaptureRemovalAdmission(ctx, dir, []string{owner, owner + ".recovery.lock"})
		if err != nil {
			return err
		}
	}
	if a.Check(ctx, dir, true) != nil {
		return domain.SessionDeletionPending()
	}
	if ownerErr == nil {
		if err := process.ReconcileOwnerContext(ctx, dir, w.SessionID); err != nil {
			return err
		}
	}
	if err := persistWorkspaceRemovalAdmission(ctx, dir, proofPath, w, &proof, a); err != nil {
		return err
	}
	for _, p := range []string{owner, owner + ".recovery.lock"} {
		if err := security.RemoveAdmittedTree(ctx, dir, p, a); err != nil {
			return err
		}
	}
	// A completed cleanup generation cannot lend authority to the next Git
	// child. Retire its own bounded metadata only after both names are absent.
	for _, p := range []string{proofPath, workspaceRemovalAdmissionPath(proofPath)} {
		if err := os.Remove(p); err != nil && !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
		if err := security.SyncParent(p); err != nil {
			return domain.SessionDeletionPending()
		}
	}
	return RequireDeletionGitRemoved(root, w)
}
