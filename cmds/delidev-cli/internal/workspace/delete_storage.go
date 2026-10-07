// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Paths derive exclusively from the immutable deletion plan, including snapshots
// reserved before native work. Missing output cannot hide an interrupted copy.
func SessionStorageCopyPaths(root string, w domain.SessionDeletionWork) []string {
	paths := []string{filepath.Join(root, "workspace-restores", string(w.SessionID)+".json"), filepath.Join(root, "workspace-read-processes-v2", string(w.SessionID))}
	seen := map[domain.ID]bool{}
	for _, copy := range w.Copies {
		if copy.Type != domain.WorkspaceStorageJob {
			continue
		}
		for _, directory := range []string{"snapshot-staging", "workspace-removals", "workspace-removal-roots"} {
			paths = append(paths, filepath.Join(root, directory, string(copy.JobID)))
		}
		// Retain publication authority until its snapshot namespace is removed,
		// so an interrupted deletion can still verify the original copy on retry.
		if copy.SnapshotID != "" && !seen[copy.SnapshotID] {
			seen[copy.SnapshotID] = true
			paths = append(paths, filepath.Join(root, "snapshots", string(copy.SnapshotID)))
		}
		for _, directory := range []string{"storage-removal-root-claims", "storage-removal-intents", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims"} {
			paths = append(paths, filepath.Join(root, directory, string(copy.JobID)+".json"))
		}
		paths = append(paths, filepath.Join(root, "storage-removal-claims", string(copy.JobID)+".pending"))
	}
	return paths
}

// A stored workspace has no live manifest. Retain its exact snapshot-bound
// manifest outside all removal paths before deleting its only managed copy.
func (m *Manager) deletionSnapshotManifest(ctx context.Context, w domain.SessionDeletionWork) (*Manifest, error) {
	var manifest *Manifest
	for _, copy := range w.Copies {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if copy.SnapshotID == "" {
			continue
		}
		path := m.snapshotPath(copy.SnapshotID)
		if _, err := os.Lstat(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, domain.SessionDeletionPending()
		}
		if err := security.CheckPrivateDir(path); err != nil {
			return nil, domain.SessionDeletionPending()
		}
		raw, err := security.ReadPrivate(filepath.Join(path, "snapshot.json"), maxSnapshotManifest)
		var snapshot snapshotManifest
		if err != nil || domain.DecodeBounded(raw, &snapshot, maxSnapshotManifest) != nil || snapshot.Version != 1 || snapshot.ID != copy.SnapshotID ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(snapshot.Workspace.SessionID), snapshot.Workspace.SessionID != w.SessionID) ||
			domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(snapshot.Workspace.MachineID), snapshot.Workspace.MachineID != w.MachineID) ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(snapshot.Preparation.SessionID), snapshot.Preparation.SessionID != w.SessionID) ||
			domain.OwnershipBlocks(domain.OwnershipMachine, domain.ID(snapshot.Preparation.MachineID), snapshot.Preparation.MachineID != w.MachineID) ||
			snapshot.Preparation.Type == domain.Local || ValidateResult(snapshot.Preparation, snapshot.Workspace, runtime.GOOS) != nil || !slices.Contains(w.PreparationDigests, snapshot.Workspace.InputDigest) {
			return nil, domain.SessionDeletionPending()
		}
		sum := sha256.Sum256(raw)
		if _, err := m.verifySnapshotPublication(snapshot, hex.EncodeToString(sum[:])); err != nil {
			return nil, domain.SessionDeletionPending()
		}
		original := slices.ContainsFunc(w.Copies, func(c domain.SessionDeletionCopy) bool {
			return c.Type == domain.WorkspaceStorageJob && c.JobID == snapshot.OperationID && c.SnapshotID == snapshot.ID
		})
		if !original || manifest != nil && manifestDigest(*manifest) != manifestDigest(snapshot.Workspace) {
			return nil, domain.SessionDeletionPending()
		}
		value := snapshot.Workspace
		manifest = &value
	}
	return manifest, nil
}

func (m *Manager) deletionRestoredWorkspace(ctx context.Context, w domain.SessionDeletionWork, manifest Manifest) (bool, error) {
	raw, err := security.ReadPrivate(m.restoreBindingPath(w.SessionID), 4096)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	var binding restoreBinding
	if err != nil || domain.Decode(raw, &binding) != nil || binding.Version != 2 || !binding.Published || !digestValid(binding.DirectoryIdentity) || !digestValid(binding.SnapshotDigest) ||
		domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(binding.SessionID), binding.SessionID != w.SessionID) ||
		binding.ManifestDigest != manifestDigest(manifest) || !digestValid(binding.OriginalIdentity) || !slices.ContainsFunc(w.Copies, func(c domain.SessionDeletionCopy) bool {
		return c.Type == domain.WorkspaceStorageJob && c.JobID == binding.OperationID && c.SnapshotID == binding.SnapshotID
	}) {
		return false, domain.SessionDeletionPending()
	}
	root := filepath.Join(m.Root, "workspaces", string(w.SessionID))
	identity, err := restoredDirectoryIdentity(root, manifest)
	if err != nil || identity != binding.DirectoryIdentity {
		return false, domain.SessionDeletionPending()
	}
	// The original snapshot may already have been explicitly deleted after a
	// successful restoration; its revision-bound binding still owns this workspace.
	for _, repo := range manifest.Repositories {
		info, err := os.Lstat(repo.Path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return false, domain.SessionDeletionPending()
		}
		git := m.Git
		git.OwnerID, git.readOnly, git.offline = w.SessionID, true, true
		fields, err := git.revParseFields(ctx, repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
		expected := filepath.Join(repo.Path, ".git")
		if err != nil || !sameNativePath(fields[0], expected) || !sameNativePath(fields[1], expected) {
			return false, domain.SessionDeletionPending()
		}
	}
	identity, err = restoredDirectoryIdentity(root, manifest)
	if err != nil || identity != binding.DirectoryIdentity {
		return false, domain.SessionDeletionPending()
	}
	return true, nil
}

// SessionStorageRemnantPaths inventories shared directories before both removal
// and completed-proof replay. Partial owned writes cannot be decoded; their
// target-bound names carry original operation authority. Legacy unowned names
// remain protected and block completion rather than silently leaking content.
func SessionStorageRemnantPaths(ctx context.Context, root string, w domain.SessionDeletionWork) ([]string, error) {
	paths := []string{}
	jobs := map[domain.ID]bool{}
	for _, copy := range w.Copies {
		if copy.Type == domain.WorkspaceStorageJob {
			jobs[copy.JobID] = true
		}
	}
	for _, directory := range []string{"workspace-restores", "storage-removal-intents", "storage-removal-root-claims", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims"} {
		path := filepath.Join(root, directory)
		if err := security.CheckPrivateDir(path); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, domain.SessionDeletionPending()
		}
		f, err := os.Open(path)
		if err != nil {
			return nil, domain.SessionDeletionPending()
		}
		// The global namespace has a separate finite observation bound. Overflow
		// preserves every entry; it never returns a truncated absence proof.
		entries, readErr := f.ReadDir(65537)
		closeErr := f.Close()
		if readErr != nil && !errors.Is(readErr, io.EOF) || closeErr != nil || len(entries) > 65536 {
			return nil, domain.SessionDeletionPending()
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			name := entry.Name()
			if !strings.HasPrefix(name, ".pending-") {
				continue
			}
			// Canonical UUID + .json + separator + CreateTemp decimal suffix.
			target := strings.TrimPrefix(name, ".pending-")
			separator := ".json-"
			if directory == "storage-removal-claims" && strings.Contains(target, ".pending-") {
				separator = ".pending-"
			}
			parts := strings.SplitN(target, separator, 2)
			if len(parts) != 2 || domain.ID(parts[0]).Validate() != nil || len(parts[1]) == 0 || len(parts[1]) > 20 {
				return nil, domain.SessionDeletionPending()
			}
			for _, ch := range parts[1] {
				if ch < '0' || ch > '9' {
					return nil, domain.SessionDeletionPending()
				}
			}
			owned := jobs[domain.ID(parts[0])]
			if directory == "workspace-restores" {
				owned = parts[0] == string(w.SessionID)
			}
			if !owned {
				continue
			}
			file := filepath.Join(path, name)
			if security.RegularPrivate(file) != nil {
				return nil, domain.SessionDeletionPending()
			}
			paths = append(paths, file)
		}
	}
	namespace, err := finalRemovalNamespaceInventory(ctx, root, jobs)
	if err != nil {
		return nil, domain.SessionDeletionPending()
	}
	for _, finalRoots := range namespace {
		paths = append(paths, finalRoots...)
	}
	// The initial deletion inventory includes canonical claim paths, but a proof
	// can be recreated after that inventory. Recheck each exact final-root proof
	// at the completion boundary so a late proof cannot be mistaken for absence.
	for jobID := range jobs {
		claim := filepath.Join(root, "storage-removal-root-claims", string(jobID)+".json")
		if _, err := os.Lstat(claim); err == nil {
			if err := security.RegularPrivate(claim); err != nil {
				return nil, domain.SessionDeletionPending()
			}
			paths = append(paths, claim)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, domain.SessionDeletionPending()
		}
	}
	return paths, nil
}

// Permanent deletion holds the session, observation and snapshot namespace gates.
// The immutable job names an obligation, not authority over a current directory.
// Keep the intent, claim and journal until the Worker verifies namespace absence.
func (m *Manager) cleanupDeletionRemovals(ctx context.Context, w domain.SessionDeletionWork) (returned error) {
	var operationID domain.ID
	stage := "namespace"
	defer func() {
		if returned != nil && m.Logger != nil {
			m.Logger.WarnContext(ctx, "workspace_deletion_removal_pending", "session_id", w.SessionID, "operation_id", operationID, "stage", stage, "code", domain.SafeError(returned).Code)
		}
	}()
	for _, copy := range w.Copies {
		if err := ctx.Err(); err != nil {
			return err
		}
		if copy.Type != domain.WorkspaceStorageJob {
			continue
		}
		operationID, stage = copy.JobID, "namespace"
		path := filepath.Join(m.Root, "workspace-removals", string(copy.JobID))
		exists, err := storageExists(path)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		if !exists {
			continue
		}
		stage = "intent"
		raw, err := security.ReadPrivate(m.removalIntentPath(copy.JobID), maxSnapshotManifest)
		var intent storageRemovalIntent
		if err != nil || domain.DecodeBounded(raw, &intent, maxSnapshotManifest) != nil || intent.Version != 1 || intent.OperationID != copy.JobID ||
			domain.OwnershipBlocks(domain.OwnershipResource, domain.ID(intent.SessionID), intent.SessionID != w.SessionID) ||
			copy.SnapshotID.Validate() != nil || intent.SnapshotID != copy.SnapshotID || (intent.Action != StorageCleanup && intent.Action != StorageDelete) || !digestValid(intent.SnapshotDigest) {
			return domain.SessionDeletionPending()
		}
		// Only these immutable references are needed. Do not reconstruct a native
		// request, inspect Git, recapture inventory or adopt a missing claim.
		request := StorageRequest{OperationID: copy.JobID, Action: intent.Action, SnapshotID: copy.SnapshotID, Preparation: PrepareRequest{SessionID: w.SessionID}}
		stage = "claim-journal"
		claim, pending, removed, err := m.readRemovalClaimState(request, raw)
		if err != nil {
			return domain.SessionDeletionPending()
		}
		stage = "root-identity"
		identity, err := directoryPathIdentity(path)
		if err != nil || identity != claim.RootIdentity {
			return domain.SessionDeletionPending()
		}
		stage = "inventory"
		if err := m.verifyRemovalInventory(ctx, request, path, true, intent, pending, removed); err != nil {
			return domain.SessionDeletionPending()
		}
		stage = "claimed-removal"
		if err := m.removeClaimedSnapshotTree(ctx, request, path, true); err != nil {
			return domain.SessionDeletionPending()
		}
	}
	return nil
}
