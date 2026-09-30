// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func (m *Manager) createSnapshot(ctx context.Context, r StorageRequest, identity string, sources snapshotInventory, expectedDigest string) (SnapshotMetadata, error) {
	var empty SnapshotMetadata
	staging := filepath.Join(m.Root, "snapshot-staging", string(r.OperationID))
	if err := os.Mkdir(staging, 0700); err != nil {
		return empty, ResultUncertain()
	}
	defer func() {
		// A scratch copy is never a published snapshot. Preserve uncertain cleanup
		// rather than letting it acquire another request's creation authority.
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := removeSnapshotTree(cleanup, staging); err != nil {
			m.Logger.Warn("snapshot_scratch_cleanup_pending", "operation_id", r.OperationID, "code", domain.SafeError(err).Code)
		}
	}()
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	skip := func(path string) bool {
		for _, repo := range r.Manifest.Repositories {
			if path == string(repo.ID)+"/.git" || strings.HasPrefix(path, string(repo.ID)+"/.git/") {
				return true
			}
		}
		return false
	}
	copied, err := walkSnapshot(ctx, root, filepath.Join(staging, "workspace"), skip)
	if err != nil {
		return empty, err
	}
	if inventoryDigest(copied) != inventoryDigest(sources) {
		return empty, ResultUncertain()
	}
	for _, repo := range r.Manifest.Repositories {
		if err := m.copySnapshotGit(ctx, r.Preparation.SessionID, repo, filepath.Join(staging, "workspace", string(repo.ID))); err != nil {
			return empty, err
		}
	}
	original, err := walkSnapshot(ctx, root, "", skip)
	if err != nil || inventoryDigest(original) != inventoryDigest(sources) {
		return empty, ResultUncertain()
	}
	observed, err := m.verifyWorkspaceIdentity(ctx, r.Preparation, r.Manifest, continuationIdentity)
	if err != nil || observed != identity {
		return empty, ResultUncertain()
	}
	inventory, err := walkSnapshot(ctx, filepath.Join(staging, "workspace"), "", nil)
	if err != nil {
		return empty, err
	}
	observation, err := m.storageObservation(ctx, r)
	if err != nil || observation.Digest != expectedDigest {
		return empty, ResultUncertain()
	}
	snapshot := snapshotManifest{SourceBytes: observation.Whole.Bytes, SourceDigest: observation.Digest, SourceInventory: observation.Whole, Version: 1, ID: r.SnapshotID, OperationID: r.OperationID, Workspace: r.Manifest, Preparation: r.Preparation, OriginalIdentity: identity, Inventory: inventory, CreatedAt: time.Now().UTC()}
	raw, err := json.Marshal(snapshot)
	if err != nil {
		return empty, err
	}
	if len(raw) > maxSnapshotManifest || uint64(len(raw))+inventory.Bytes > MaxSnapshotBytes {
		return empty, domain.Fail(domain.ResourceExhausted, "The complete snapshot exceeds its bound.", "Reduce workspace data before cleanup; source files remain intact.")
	}
	if err := security.WriteAtomic(filepath.Join(staging, "snapshot.json"), raw); err != nil {
		return empty, err
	}
	if err := syncSnapshotDir(staging); err != nil {
		return empty, err
	}
	if err := ctx.Err(); err != nil {
		return empty, err
	}
	if err := renameStorage(staging, m.snapshotPath(r.SnapshotID)); err != nil {
		return empty, ResultUncertain()
	}
	_, metadata, err := m.inspectSnapshot(ctx, r.SnapshotID)
	if err != nil {
		return empty, ResultUncertain()
	}
	return metadata, nil
}

func (m *Manager) copySnapshotGit(ctx context.Context, session domain.ID, repo PreparedRepository, target string) error {
	if m.storageCopyFault != nil {
		if err := m.storageCopyFault(target); err != nil {
			return err
		}
	}
	git := m.Git
	git.OwnerID = session
	git.readOnly = true
	git.offline = true
	fields, err := git.revParseFields(ctx, repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		return err
	}
	common, admin := fields[0], fields[1]
	if !filepath.IsAbs(common) || !filepath.IsAbs(admin) {
		return ResultUncertain()
	}
	for _, path := range []string{common, admin} {
		info, err := os.Lstat(path)
		if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return ResultUncertain()
		}
	}
	// External alternates, config includes and submodule Git pointers cannot be
	// promised self-contained recovery. Refuse them instead of omitting content.
	if _, err := os.Lstat(filepath.Join(common, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		return snapshotUnsupported()
	}
	_, exit, err := git.runCommand(ctx, repo.Path, "config", "--no-includes", "--local", "--get-regexp", "^(include|includeIf)\\.")
	if err == nil || exit != 1 {
		return snapshotUnsupported()
	}
	worktreeConfigSource := filepath.Join(admin, "config.worktree")
	if _, err := os.Lstat(worktreeConfigSource); err == nil {
		_, exit, err := git.runCommand(ctx, repo.Path, "config", "--no-includes", "--file", worktreeConfigSource, "--get-regexp", "^(include|includeIf)\\.")
		if err == nil || exit != 1 {
			return snapshotUnsupported()
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	adminInventory, err := walkSnapshot(ctx, admin, "", nil)
	if err != nil {
		return err
	}
	if err := validateGitInventory(adminInventory); err != nil {
		return err
	}
	adminFiles := map[string]bool{}
	if !sameNativePath(common, admin) {
		for _, entry := range adminInventory.Entries {
			if os.FileMode(entry.Mode)&os.ModeDir == 0 {
				adminFiles[entry.Path] = true
			}
		}
	}
	skipCommon := func(path string) bool {
		return path == "worktrees" || strings.HasPrefix(path, "worktrees/") || (!sameNativePath(common, admin) && (path == "logs" || strings.HasPrefix(path, "logs/") || adminFiles[path]))
	}
	commonInventory, err := walkSnapshot(ctx, common, filepath.Join(target, ".git"), skipCommon)
	if err != nil {
		return err
	}
	if err := validateGitInventory(commonInventory); err != nil {
		return err
	}
	if !sameNativePath(common, admin) {
		skipAdmin := func(path string) bool { return path == "commondir" || path == "gitdir" || path == "locked" }
		if _, err := walkSnapshot(ctx, admin, filepath.Join(target, ".git"), skipAdmin, true); err != nil {
			return err
		}
	}
	// A relative worktree location keeps the independent Git store relocatable;
	// original source checkouts, refs and administrative files are never changed.
	if _, err := git.run(ctx, target, "config", "--local", "core.bare", "false"); err != nil {
		return err
	}
	if _, err := git.run(ctx, target, "config", "--local", "core.worktree", ".."); err != nil {
		return err
	}
	worktreeConfig := filepath.Join(target, ".git", "config.worktree")
	if _, err := os.Lstat(worktreeConfig); err == nil {
		if _, err := git.run(ctx, target, "config", "--file", worktreeConfig, "core.worktree", ".."); err != nil {
			return err
		}
		if _, err := git.run(ctx, target, "config", "--file", worktreeConfig, "core.bare", "false"); err != nil {
			return err
		}
	}
	if err := m.validateSnapshotGit(ctx, session, repo, target); err != nil {
		return err
	}
	commonAfter, err := walkSnapshot(ctx, common, "", skipCommon)
	if err != nil || inventoryDigest(commonAfter) != inventoryDigest(commonInventory) {
		return ResultUncertain()
	}
	adminAfter, err := walkSnapshot(ctx, admin, "", nil)
	if err != nil || inventoryDigest(adminAfter) != inventoryDigest(adminInventory) {
		return ResultUncertain()
	}
	// Git config updates are fsynced explicitly; process completion alone cannot
	// prove that a recoverable copy has reached stable storage. Windows flushes
	// require a write-capable handle; open only the independently copied config.
	for _, config := range []string{"config", "config.worktree"} {
		path := filepath.Join(target, ".git", config)
		file, err := os.OpenFile(path, os.O_RDWR, 0)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return syncSnapshotDir(filepath.Join(target, ".git"))
}

type snapshotGitValidationPhase string

const (
	snapshotGitCommits  snapshotGitValidationPhase = "commits"
	snapshotGitObjects  snapshotGitValidationPhase = "objects"
	snapshotGitLocation snapshotGitValidationPhase = "location"
)

func (m *Manager) validateSnapshotGit(ctx context.Context, session domain.ID, repo PreparedRepository, path string) (returned error) {
	phase := snapshotGitCommits
	defer func() {
		if returned != nil {
			m.Logger.Warn("snapshot_git_validation_failed", "session_id", session, "repository_id", repo.ID, "phase", phase, "code", domain.SafeError(returned).Code)
		}
	}()
	git := m.Git
	git.OwnerID = session
	git.readOnly = true
	git.offline = true
	for _, commit := range []string{repo.BaseCommit, repo.StartingCommit, "HEAD"} {
		if _, err := git.run(ctx, path, "cat-file", "-e", commit+"^{commit}"); err != nil {
			return ResultUncertain()
		}
	}
	phase = snapshotGitObjects
	if _, err := git.run(ctx, path, "fsck", "--full", "--no-dangling"); err != nil {
		return ResultUncertain()
	}
	// Native index references, including staged conflict entries, must resolve
	// locally. fsck verifies the index without running clean/textconv filters.
	phase = snapshotGitLocation
	fields, err := git.revParseFields(ctx, path, 2, "--git-common-dir", "--absolute-git-dir")
	expected := filepath.Join(path, ".git")
	if err != nil || !sameNativePath(fields[0], expected) || !sameNativePath(fields[1], expected) {
		return ResultUncertain()
	}
	return nil
}
func (m *Manager) inspectSnapshot(ctx context.Context, id domain.ID) (snapshotManifest, SnapshotMetadata, error) {
	var snapshot snapshotManifest
	var metadata SnapshotMetadata
	if id.Validate() != nil {
		return snapshot, metadata, ResultUncertain()
	}
	root := m.snapshotPath(id)
	if err := security.CheckPrivateDir(root); err != nil {
		return snapshot, metadata, err
	}
	raw, err := security.ReadPrivate(filepath.Join(root, "snapshot.json"), maxSnapshotManifest)
	if err != nil {
		return snapshot, metadata, err
	}
	if domain.Decode(raw, &snapshot) != nil || snapshot.Version != 1 || snapshot.ID != id || snapshot.OperationID.Validate() != nil || snapshot.Preparation.Type == domain.Local || ValidateResult(snapshot.Preparation, snapshot.Workspace, runtime.GOOS) != nil || !digestValid(snapshot.OriginalIdentity) || snapshot.CreatedAt.IsZero() {
		return snapshot, metadata, ResultUncertain()
	}
	inventory, err := walkSnapshot(ctx, filepath.Join(root, "workspace"), "", nil)
	if err != nil || inventoryDigest(inventory) != inventoryDigest(snapshot.Inventory) {
		return snapshot, metadata, ResultUncertain()
	}
	for _, repo := range snapshot.Workspace.Repositories {
		if err := m.validateSnapshotGit(ctx, snapshot.Workspace.SessionID, repo, filepath.Join(root, "workspace", string(repo.ID))); err != nil {
			return snapshot, metadata, err
		}
	}
	sum := sha256.Sum256(raw)
	metadata = SnapshotMetadata{ID: id, SessionID: snapshot.Workspace.SessionID, MachineID: snapshot.Workspace.MachineID, SHA256: hex.EncodeToString(sum[:]), SizeBytes: inventory.Bytes + uint64(len(raw)), CreatedAt: snapshot.CreatedAt, RepositoryCount: uint32(len(snapshot.Workspace.Repositories))}
	return snapshot, metadata, nil
}
func (m *Manager) snapshotBytes(ctx context.Context, session domain.ID) (uint64, error) {
	root := filepath.Join(m.Root, "snapshots")
	dir, err := os.Open(root)
	if err != nil {
		return 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(4097)
	if err != nil && err != io.EOF {
		return 0, err
	}
	if len(entries) > 4096 {
		return 0, ResultUncertain()
	}
	var total uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		id := domain.ID(entry.Name())
		if id.Validate() != nil || !entry.IsDir() {
			return 0, ResultUncertain()
		}
		raw, err := security.ReadPrivate(filepath.Join(root, entry.Name(), "snapshot.json"), maxSnapshotManifest)
		if err != nil {
			return 0, err
		}
		var manifest snapshotManifest
		if domain.Decode(raw, &manifest) != nil || manifest.ID != id {
			return 0, ResultUncertain()
		}
		if manifest.Workspace.SessionID == session {
			if manifest.Inventory.Bytes > MaxSnapshotBytes || total > ^uint64(0)-manifest.Inventory.Bytes-uint64(len(raw)) {
				return 0, ResultUncertain()
			}
			total += manifest.Inventory.Bytes + uint64(len(raw))
		}
	}
	return total, nil
}

// Preview and cleanup compare both filesystem data and the original Git
// administrative/object inventories. An index-only change must stale a preview
// even when every worktree file has the same bytes.
type storageObservationResult struct {
	Data   snapshotInventory
	Whole  snapshotInventory
	Digest string
}

func (m *Manager) storageObservation(ctx context.Context, r StorageRequest) (storageObservationResult, error) {
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	skip := func(path string) bool {
		for _, repo := range r.Manifest.Repositories {
			if path == string(repo.ID)+"/.git" || strings.HasPrefix(path, string(repo.ID)+"/.git/") {
				return true
			}
		}
		return false
	}
	data, err := walkSnapshot(ctx, root, "", skip)
	if err != nil {
		return storageObservationResult{}, err
	}
	whole, err := walkSnapshot(ctx, root, "", nil)
	if err != nil {
		return storageObservationResult{}, err
	}
	observations := []snapshotInventory{whole}
	git := m.Git
	git.OwnerID = r.Preparation.SessionID
	git.readOnly = true
	git.offline = true
	for _, repo := range r.Manifest.Repositories {
		fields, err := git.revParseFields(ctx, repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
		if err != nil {
			return storageObservationResult{}, err
		}
		for i, path := range fields {
			if i == 1 && sameNativePath(path, fields[0]) {
				continue
			}
			inventory, err := walkSnapshot(ctx, path, "", func(path string) bool { return path == "worktrees" || strings.HasPrefix(path, "worktrees/") })
			if err != nil {
				return storageObservationResult{}, err
			}
			observations = append(observations, inventory)
		}
	}
	raw, err := json.Marshal(observations)
	if err != nil {
		return storageObservationResult{}, err
	}
	sum := sha256.Sum256(raw)
	return storageObservationResult{Data: data, Whole: whole, Digest: hex.EncodeToString(sum[:])}, nil
}

// Git follows links inside its administration directories. Such links cannot
// enter a self-contained snapshot, even though ordinary workspace links are safe.
func validateGitInventory(inventory snapshotInventory) error {
	for _, entry := range inventory.Entries {
		if os.FileMode(entry.Mode)&os.ModeSymlink != 0 {
			return snapshotUnsupported()
		}
	}
	return nil
}
