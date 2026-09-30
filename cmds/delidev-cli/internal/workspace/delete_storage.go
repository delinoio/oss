// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Paths derive exclusively from the immutable deletion plan, including snapshots
// reserved before native work. Missing output cannot hide an interrupted copy.
func SessionStorageCopyPaths(root string, w domain.SessionDeletionWork) []string {
	paths := []string{filepath.Join(root, "workspace-restores", string(w.SessionID)+".json")}
	seen := map[domain.ID]bool{}
	for _, copy := range w.Copies {
		if copy.Type != domain.WorkspaceStorageJob {
			continue
		}
		for _, directory := range []string{"snapshot-staging", "workspace-removals"} {
			paths = append(paths, filepath.Join(root, directory, string(copy.JobID)))
		}
		for _, directory := range []string{"storage-removal-intents", "storage-removal-claims", "storage-removal-retirements"} {
			paths = append(paths, filepath.Join(root, directory, string(copy.JobID)+".json"))
		}
		if copy.SnapshotID != "" && !seen[copy.SnapshotID] {
			seen[copy.SnapshotID] = true
			paths = append(paths, filepath.Join(root, "snapshots", string(copy.SnapshotID)))
		}
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
		if err != nil || domain.DecodeBounded(raw, &snapshot, maxSnapshotManifest) != nil || snapshot.Version != 1 || snapshot.ID != copy.SnapshotID || snapshot.Workspace.SessionID != w.SessionID || snapshot.Workspace.MachineID != w.MachineID || snapshot.Preparation.SessionID != w.SessionID || snapshot.Preparation.MachineID != w.MachineID || snapshot.Preparation.Type == domain.Local || ValidateResult(snapshot.Preparation, snapshot.Workspace, runtime.GOOS) != nil || !slices.Contains(w.PreparationDigests, snapshot.Workspace.InputDigest) {
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
	if err != nil || domain.Decode(raw, &binding) != nil || binding.Version != 1 || binding.SessionID != w.SessionID || binding.ManifestDigest != manifestDigest(manifest) || !digestValid(binding.OriginalIdentity) || !slices.ContainsFunc(w.Copies, func(c domain.SessionDeletionCopy) bool { return c.SnapshotID == binding.SnapshotID }) {
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
	return true, nil
}
