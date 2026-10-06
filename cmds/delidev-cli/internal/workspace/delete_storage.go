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
		for _, directory := range []string{"storage-removal-intents", "storage-removal-root-claims", "storage-removal-claims", "storage-removal-retirements", "storage-staging-claims"} {
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
		if err != nil || domain.DecodeBounded(raw, &snapshot, maxSnapshotManifest) != nil || snapshot.Version != 1 || snapshot.ID != copy.SnapshotID || snapshot.Workspace.SessionID != w.SessionID || snapshot.Workspace.MachineID != w.MachineID || snapshot.Preparation.SessionID != w.SessionID || snapshot.Preparation.MachineID != w.MachineID || snapshot.Preparation.Type == domain.Local || ValidateResult(snapshot.Preparation, snapshot.Workspace, runtime.GOOS) != nil || !slices.Contains(w.PreparationDigests, snapshot.Workspace.InputDigest) {
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
	if err != nil || domain.Decode(raw, &binding) != nil || binding.Version != 2 || !binding.Published || !digestValid(binding.DirectoryIdentity) || !digestValid(binding.SnapshotDigest) || binding.SessionID != w.SessionID || binding.ManifestDigest != manifestDigest(manifest) || !digestValid(binding.OriginalIdentity) || !slices.ContainsFunc(w.Copies, func(c domain.SessionDeletionCopy) bool {
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
	finalRoots, err := finalRemovalNamespacePaths(ctx, root, jobs)
	if err != nil {
		return nil, domain.SessionDeletionPending()
	}
	return append(paths, finalRoots...), nil
}
