// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

type sessionRemovalAdmission struct {
	Version  uint32                         `json:"version"`
	Identity domain.SessionDeletionIdentity `json:"identity"`
	Digest   string                         `json:"digest"`
	ReportID domain.ID                      `json:"report_id"`
	Native   *security.RemovalAdmission     `json:"native"`
}

func sessionRemovalAdmissionPath(proofPath string) string {
	return strings.TrimSuffix(proofPath, ".json") + "-admission.json"
}
func loadSessionRemovalAdmission(root, path string, w domain.SessionDeletionWork, proof sessionDeletionProof) (*security.RemovalAdmission, string, error) {
	raw, err := security.ReadPrivate(sessionRemovalAdmissionPath(path), security.MaxRemovalAdmissionBytes)
	if errors.Is(err, os.ErrNotExist) && proof.AdmissionDigest == "" {
		return nil, "", nil
	}
	var record sessionRemovalAdmission
	if err != nil || domain.DecodeWithLimit(raw, &record, security.MaxRemovalAdmissionBytes) != nil || record.Version != 1 || record.Identity != w.Identity() || record.Digest != w.Digest() || record.ReportID != proof.ReportID || record.Native.Validate() != nil {
		return nil, "", domain.SessionDeletionPending()
	}
	digest := executionInputDigest(raw)
	if proof.AdmissionDigest != "" && proof.AdmissionDigest != digest {
		return nil, "", domain.SessionDeletionPending()
	}
	if proof.RemovalStarted && proof.AdmissionDigest == "" {
		return nil, "", domain.SessionDeletionPending()
	}
	return record.Native, digest, nil
}
func persistSessionRemovalAdmission(ctx context.Context, root, path string, w domain.SessionDeletionWork, proof *sessionDeletionProof, a *security.RemovalAdmission) error {
	if a.CheckTrees(ctx, root) != nil {
		return domain.SessionDeletionPending()
	}
	if proof.AdmissionDigest == "" {
		record := sessionRemovalAdmission{1, w.Identity(), w.Digest(), proof.ReportID, a}
		raw, err := json.Marshal(record)
		if err != nil || len(raw) > security.MaxRemovalAdmissionBytes {
			return domain.SessionDeletionPending()
		}
		if err := security.WriteAtomic(sessionRemovalAdmissionPath(path), raw); err != nil {
			return domain.SessionDeletionPending()
		}
		proof.AdmissionDigest = executionInputDigest(raw)
	}
	if !proof.RemovalStarted {
		proof.RemovalStarted = true
		return writeJSON(path, *proof)
	}
	return nil
}

// These independently removed namespaces are absence-only at generic cleanup.
func independentSessionCopy(root, path string) bool {
	parent := filepath.Dir(path)
	name := filepath.Base(path)
	finalClaim := parent == filepath.Join(root, "storage-removal-root-claims") && len(name) == 41 && name[36:] == ".json" && domain.ID(name[:36]).Validate() == nil
	return finalClaim || parent == filepath.Join(root, "snapshot-staging") || parent == filepath.Join(root, "workspace-removals") || parent == filepath.Join(root, "workspace-removal-roots") || parent == filepath.Join(root, "workspace-removal-quarantine") || parent == filepath.Join(root, "skill-snapshots")
}
func genericSessionAdmissionPaths(ctx context.Context, root string, w domain.SessionDeletionWork) ([]string, error) {
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	independent := map[string]bool{}
	for _, copy := range w.Copies {
		if copy.UnpublishedChildProcessID != "" {
			independent[filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID))] = true
			independent[filepath.Join(root, "processes", string(copy.UnpublishedChildProcessID)+".recovery.lock")] = true
		}
		if copy.UnpublishedSidechatID != "" {
			independent[filepath.Join(root, "workspaces", string(copy.UnpublishedSidechatID))] = true
		}
	}
	admitted := []string{}
	for _, path := range paths {
		if !independentSessionCopy(root, path) && !independent[path] {
			admitted = append(admitted, path)
		}
	}
	if len(w.Copies) != 0 || w.Fork != nil {
		admitted = append(admitted, filepath.Join(root, "workspaces", string(w.SessionID)))
	}
	return admitted, nil
}

func completeLegacySessionDeletion(ctx context.Context, config Config, root, path string, w domain.SessionDeletionWork, proof sessionDeletionProof) (sessionDeletionProof, error) {
	if err := workspace.RequireDeletionGitRemoved(root, w); err != nil {
		return proof, err
	}
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return proof, err
	}
	if len(w.Copies) != 0 || w.Fork != nil {
		paths = append(paths, filepath.Join(root, "workspaces", string(w.SessionID)))
	}
	if w.Fork != nil {
		paths = append(paths, workspace.SidechatForkClaimPath(root, w.Fork.JobID))
	}
	for _, p := range paths {
		if _, err := os.Lstat(p); !errors.Is(err, os.ErrNotExist) {
			proof.Complete = false
			return proof, domain.SessionDeletionPending()
		}
	}
	if err := cleanupGeneratedCopies(root, w, true); err != nil {
		return proof, err
	}
	for _, ref := range w.Images {
		if err := (imageinput.Manager{Root: root}).Removed(w.MachineID, ref); err != nil {
			return proof, err
		}
	}
	proof.Complete = true
	if err := writeJSON(path, proof); err != nil {
		return proof, err
	}
	return finishSessionDeletionProof(root, path, w, proof)
}

// Establish independent reconciliation-lock metadata before native admission.
// No ownership reader runs here; pin the original directory/root across lock
// creation so this prerequisite cannot silently substitute an owner tree.
func prepareSessionDeletionRecoveryLocks(ctx context.Context, root string, w domain.SessionDeletionWork) error {
	owners := []domain.ID{w.SessionID}
	for _, copy := range w.Copies {
		owners = append(owners, copy.JobID)
	}
	for _, owner := range owners {
		dir := filepath.Join(root, "processes", string(owner))
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return domain.SessionDeletionPending()
		}
		pin, err := security.CaptureRemovalAdmission(ctx, root, []string{dir})
		if err != nil {
			return err
		}
		lock, err := security.TryLock(dir + ".recovery.lock")
		if err != nil {
			return domain.SessionDeletionPending()
		}
		err = lock.Close()
		if err != nil || pin.Check(ctx, root, false) != nil {
			return domain.SessionDeletionPending()
		}
	}
	// Existing publication directories need their lock file before full tree
	// capture. Preserve the original directory while establishing this metadata;
	// otherwise later TryLock could introduce an unadmitted name during cleanup.
	for _, copy := range w.Copies {
		dir := filepath.Join(root, "jobs", string(copy.JobID))
		if _, err := os.Lstat(dir); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return domain.SessionDeletionPending()
		}
		pin, err := security.CaptureRemovalAdmission(ctx, root, []string{dir})
		if err != nil {
			return err
		}
		lock, err := security.TryLock(filepath.Join(dir, "publication.lock"))
		if err != nil {
			return domain.SessionDeletionPending()
		}
		if err := lock.Close(); err != nil || pin.Check(ctx, root, false) != nil {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}

func requireSessionDeletionAbsent(ctx context.Context, root string, w domain.SessionDeletionWork) error {
	if err := workspace.RequireDeletionGitRemoved(root, w); err != nil {
		return err
	}
	paths, err := sessionDeletionCopyPaths(ctx, root, w)
	if err != nil {
		return err
	}
	if len(w.Copies) != 0 || w.Fork != nil {
		paths = append(paths, filepath.Join(root, "workspaces", string(w.SessionID)))
	}
	if w.Fork != nil {
		paths = append(paths, workspace.SidechatForkClaimPath(root, w.Fork.JobID))
	}
	for _, path := range paths {
		if err := ctx.Err(); err != nil {
			return domain.SafeError(err)
		}
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}

// Pin shared namespaces before enumerating dynamic title/storage operands. The
// optional hook exercises this earlier boundary in deterministic fixtures only.
func captureSessionRemovalAdmission(ctx context.Context, root string, w domain.SessionDeletionWork, beforeInventory func()) (*security.RemovalAdmission, error) {
	namespaces := []string{"title-runtimes", "workspace-restores", "storage-removal-intents", "storage-removal-root-claims", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims", "workspace-removal-roots", "workspace-removal-quarantine"}
	paths := make([]string, 0, len(namespaces))
	for _, namespace := range namespaces {
		paths = append(paths, filepath.Join(root, namespace))
	}
	pin, err := security.CaptureRemovalNamespaces(ctx, root, paths)
	if err != nil {
		return nil, err
	}
	if beforeInventory != nil {
		beforeInventory()
	}
	if err := pin.Check(ctx, root, false); err != nil {
		return nil, err
	}
	operands, err := genericSessionAdmissionPaths(ctx, root, w)
	if err != nil {
		return nil, err
	}
	return pin.Extend(ctx, root, operands)
}
