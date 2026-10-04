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

func (m *Manager) createSnapshot(ctx context.Context, r StorageRequest, identity string, sources snapshotInventory, expectedDigest string) (metadata SnapshotMetadata, returned error) {
	var empty SnapshotMetadata
	_, count, err := m.snapshotInventoryBytesLocked(ctx, r.Preparation.SessionID)
	if err != nil {
		return empty, err
	}
	if count >= maxPublishedSnapshots {
		return empty, domain.Fail(domain.ResourceExhausted, "The Worker snapshot inventory is full.", "Delete an unneeded verified snapshot before requesting another; sources remain intact.")
	}
	staging, err := m.createStorageStaging(r)
	if err != nil {
		return empty, err
	}
	defer func() {
		// A scratch copy is never a published snapshot. Preserve uncertain cleanup
		// rather than letting it acquire another request's creation authority.
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		err := error(nil)
		if m.storageScratchCleanupFault != nil {
			err = m.storageScratchCleanupFault(staging)
		} else {
			err = m.cleanupStorageStaging(cleanup, r)
		}
		if err != nil {
			returned = ResultUncertain()
			m.Logger.Warn("snapshot_scratch_cleanup_pending", "operation_id", r.OperationID, "code", domain.SafeError(err).Code)
		}
	}()
	root := filepath.Join(m.Root, "workspaces", string(r.Preparation.SessionID))
	sourceDirectories, err := sourceWorkspaceDirectoryIdentity(root, r.Manifest)
	if err != nil {
		return empty, err
	}
	skip := func(path string) bool {
		for _, repo := range r.Manifest.Repositories {
			if path == string(repo.ID)+"/.git" || strings.HasPrefix(path, string(repo.ID)+"/.git/") {
				return true
			}
		}
		return false
	}
	// Reserve bounded manifest and config-rewrite headroom before writing any
	// payload. Git config can add core.worktree/core.bare in two copied files;
	// 256 bytes per repository bounds those additions without touching sources.
	budget := snapshotCopyBudget{bytes: MaxSnapshotBytes - maxSnapshotManifest - 256*uint64(len(r.Manifest.Repositories)), entries: MaxSnapshotEntries, privatePathLimit: snapshotPrivatePathLimit(m.Root)}
	copied, err := walkSnapshotBudget(ctx, root, filepath.Join(staging, "workspace"), skip, MaxSnapshotEntries, &budget)
	if err != nil {
		return empty, err
	}
	if inventoryDigest(copied) != inventoryDigest(sources) {
		return empty, ResultUncertain()
	}
	for _, repo := range r.Manifest.Repositories {
		if err := m.copySnapshotGitBudget(ctx, r.Preparation.SessionID, repo, filepath.Join(staging, "workspace", string(repo.ID)), &budget); err != nil {
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
	currentDirectories, err := sourceWorkspaceDirectoryIdentity(root, r.Manifest)
	if err != nil || currentDirectories != sourceDirectories {
		return empty, ResultUncertain()
	}
	snapshot := snapshotManifest{SourceDirectoryIdentity: sourceDirectories, SourceBytes: observation.Whole.Bytes, SourceDigest: observation.Digest, SourceInventory: observation.Whole, Version: 1, ID: r.SnapshotID, OperationID: r.OperationID, Workspace: r.Manifest, Preparation: r.Preparation, OriginalIdentity: identity, Inventory: inventory, CreatedAt: time.Now().UTC()}
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
	if m.storageBeforeSnapshotPublish != nil {
		m.storageBeforeSnapshotPublish(staging)
	}
	if err := renameStorage(staging, m.snapshotPath(r.SnapshotID)); err != nil {
		return empty, ResultUncertain()
	}
	_, metadata, err = m.inspectSnapshotContent(ctx, r.SnapshotID)
	if err != nil {
		return empty, ResultUncertain()
	}
	sum := sha256.Sum256(raw)
	if metadata.SHA256 != hex.EncodeToString(sum[:]) || m.retainSnapshotPublication(r, metadata) != nil {
		return empty, ResultUncertain()
	}
	return metadata, nil
}

func (m *Manager) copySnapshotGit(ctx context.Context, session domain.ID, repo PreparedRepository, target string) error {
	budget := snapshotCopyBudget{bytes: MaxSnapshotBytes - maxSnapshotManifest - 256, entries: MaxSnapshotEntries, privatePathLimit: snapshotPrivatePathLimit(m.Root)}
	return m.copySnapshotGitBudget(ctx, session, repo, target, &budget)
}

func (m *Manager) copySnapshotGitBudget(ctx context.Context, session domain.ID, repo PreparedRepository, target string, budget *snapshotCopyBudget) error {
	if m.storageCopyFault != nil {
		if err := m.storageCopyFault(target); err != nil {
			return err
		}
	}
	git := m.Git
	git.OwnerID = session
	git.readOnly = true
	git.offline = true
	if err := validateSnapshotGitEntry(repo.Path); err != nil {
		return err
	}
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
	if err := validateSnapshotGitConfig(ctx, git, repo.Path, admin); err != nil {
		return err
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
	// Overlay only this worktree's colliding administration files (including
	// logs/HEAD). Shared branch reflogs must survive: they may be the only
	// retained reference to an unpushed commit after reset. Other worktrees'
	// private administration is unrelated to this independent Git store.
	skipCommon := func(path string) bool {
		return path == "worktrees" || strings.HasPrefix(path, "worktrees/") || adminFiles[path]
	}
	// The copied Git root appears in the final workspace inventory although
	// walking its contents excludes the root itself.
	if err := budget.take(0); err != nil {
		return err
	}
	commonInventory, err := walkSnapshotBudget(ctx, common, filepath.Join(target, ".git"), skipCommon, MaxSnapshotEntries, budget)
	if err != nil {
		return err
	}
	if err := validateGitInventory(commonInventory); err != nil {
		return err
	}
	if !sameNativePath(common, admin) {
		skipAdmin := func(path string) bool { return path == "commondir" || path == "gitdir" || path == "locked" }
		if _, err := walkSnapshotBudget(ctx, admin, filepath.Join(target, ".git"), skipAdmin, MaxSnapshotEntries, budget, true); err != nil {
			return err
		}
	}
	// Git can recreate a missing local config. Its bounded bytes have reserved
	// headroom, but the new payload file also needs a complete-inventory slot.
	if _, err := os.Lstat(filepath.Join(target, ".git", "config")); errors.Is(err, os.ErrNotExist) {
		if err := budget.take(0); err != nil {
			return err
		}
	} else if err != nil {
		return err
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
	if err := validateSnapshotGitConfig(ctx, git, path, filepath.Join(path, ".git")); err != nil {
		return err
	}
	inventory, err := walkSnapshot(ctx, filepath.Join(path, ".git"), "", nil)
	if err != nil {
		return err
	}
	if err := validateGitInventory(inventory); err != nil {
		return err
	}
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
	snapshot, metadata, err := m.inspectSnapshotContent(ctx, id)
	if err == nil {
		_, err = m.verifySnapshotPublication(snapshot, metadata.SHA256)
	}
	return snapshot, metadata, err
}

// Content inspection alone grants no snapshot ownership or removal authority.
func (m *Manager) inspectSnapshotContent(ctx context.Context, id domain.ID) (snapshotManifest, SnapshotMetadata, error) {
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
	if domain.DecodeBounded(raw, &snapshot, maxSnapshotManifest) != nil || snapshot.Version != 1 || snapshot.ID != id || snapshot.OperationID.Validate() != nil || snapshot.Preparation.Type == domain.Local || ValidateResult(snapshot.Preparation, snapshot.Workspace, runtime.GOOS) != nil || !digestValid(snapshot.OriginalIdentity) || snapshot.SourceDirectoryIdentity != "" && !digestValid(snapshot.SourceDirectoryIdentity) || snapshot.CreatedAt.IsZero() {
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
func (m *Manager) snapshotBytesLocked(ctx context.Context, session domain.ID) (uint64, error) {
	bytes, _, err := m.snapshotInventoryBytesLocked(ctx, session)
	return bytes, err
}

func (m *Manager) lockSnapshotNamespace() (*security.Lock, error) {
	return security.TryLock(filepath.Join(m.Root, "locks", "snapshot-publication.lock"))
}

func (m *Manager) snapshotInventoryBytes(ctx context.Context, session domain.ID) (uint64, int, error) {
	gate, err := m.lockSnapshotNamespace()
	if err != nil {
		return 0, 0, err
	}
	defer gate.Close()
	return m.snapshotInventoryBytesLocked(ctx, session)
}

// The caller holds the cross-process snapshot namespace gate through both
// native publication/removal and this observation of every retained snapshot.
func (m *Manager) snapshotInventoryBytesLocked(ctx context.Context, session domain.ID) (uint64, int, error) {
	root := filepath.Join(m.Root, "snapshots")
	dir, err := os.Open(root)
	if err != nil {
		return 0, 0, err
	}
	defer dir.Close()
	entries, err := dir.ReadDir(maxPublishedSnapshots + 1)
	if err != nil && err != io.EOF {
		return 0, 0, err
	}
	if len(entries) > maxPublishedSnapshots {
		return 0, 0, ResultUncertain()
	}
	var total uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return 0, 0, err
		}
		id := domain.ID(entry.Name())
		if id.Validate() != nil || !entry.IsDir() {
			return 0, 0, ResultUncertain()
		}
		raw, err := security.ReadPrivate(filepath.Join(root, entry.Name(), "snapshot.json"), maxSnapshotManifest)
		if err != nil {
			return 0, 0, err
		}
		var manifest snapshotManifest
		if domain.DecodeBounded(raw, &manifest, maxSnapshotManifest) != nil || manifest.ID != id {
			return 0, 0, ResultUncertain()
		}
		if manifest.Workspace.SessionID == session {
			if manifest.Inventory.Bytes > MaxSnapshotBytes || total > ^uint64(0)-manifest.Inventory.Bytes-uint64(len(raw)) {
				return 0, 0, ResultUncertain()
			}
			total += manifest.Inventory.Bytes + uint64(len(raw))
		}
	}
	return total, len(entries), nil
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
	budget := snapshotCopyBudget{bytes: MaxSnapshotBytes, entries: MaxSnapshotEntries, privatePathLimit: snapshotPrivatePathLimit(m.Root)}
	whole, err := walkSnapshotBudget(ctx, root, "", nil, MaxSnapshotEntries, &budget)
	if err != nil {
		return storageObservationResult{}, err
	}
	observations := []snapshotInventory{whole}
	git := m.Git
	git.OwnerID = r.Preparation.SessionID
	git.readOnly = true
	git.offline = true
	for _, repo := range r.Manifest.Repositories {
		if err := validateSnapshotGitEntry(repo.Path); err != nil {
			return storageObservationResult{}, err
		}
		fields, err := git.revParseFields(ctx, repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
		if err != nil {
			return storageObservationResult{}, err
		}
		for i, path := range fields {
			if i == 1 && sameNativePath(path, fields[0]) {
				continue
			}
			// Restored repositories keep their independent Git store inside the
			// workspace. The complete root inventory already covers its bytes,
			// entries and digest; only external original stores need another walk.
			external, err := snapshotExternalGitStore(root, path)
			if err != nil {
				return storageObservationResult{}, err
			}
			if !external {
				continue
			}
			inventory, err := walkSnapshotBudget(ctx, path, "", func(path string) bool { return path == "worktrees" || strings.HasPrefix(path, "worktrees/") }, MaxSnapshotEntries, &budget)
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

// Inspect the original entry before Git resolves its administrative paths.
// A linked worktree's regular pointer file is supported; a symlink is not.
func validateSnapshotGitEntry(repository string) error {
	root, err := os.OpenRoot(repository)
	if err != nil {
		return err
	}
	defer root.Close()
	info, err := root.Lstat(".git")
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 || (!info.IsDir() && !info.Mode().IsRegular()) {
		return snapshotUnsupported()
	}
	return nil
}

// Git follows links inside its administration directories. Such links cannot
// enter a self-contained snapshot, even though ordinary workspace links are safe.
func validateGitInventory(inventory snapshotInventory) error {
	for _, entry := range inventory.Entries {
		if os.FileMode(entry.Mode)&os.ModeSymlink != 0 || (strings.HasPrefix(entry.Path, "objects/pack/") && strings.HasSuffix(entry.Path, ".promisor")) {
			return snapshotUnsupported()
		}
	}
	return nil
}

// Promisor fsck deliberately tolerates missing promised objects. A private offline
// snapshot must reject that policy, including markers left after config removal,
// rather than treating a successful fsck as complete local object recovery.
func validateSnapshotGitConfig(ctx context.Context, git Git, path, admin string) error {
	pattern := `^(include|include[iI]f)\.|^extensions\.partialclone$|^remote\..*\.(promisor|partialclonefilter)$`
	configs := [][]string{{"--local"}}
	worktree := filepath.Join(admin, "config.worktree")
	if _, err := os.Lstat(worktree); err == nil {
		configs = append(configs, []string{"--file", worktree})
	} else if !errors.Is(err, os.ErrNotExist) {
		return ResultUncertain()
	}
	for _, config := range configs {
		args := append([]string{"config", "--no-includes"}, config...)
		args = append(args, "--get-regexp", pattern)
		_, exit, err := git.runCommand(ctx, path, args...)
		if err == nil {
			return snapshotUnsupported()
		}
		if exit != 1 {
			return ResultUncertain()
		}
	}
	return nil
}

// Rel cannot compare Windows paths on different volumes. Only that closed case
// denotes an external store; other relative-path failures remain errors.
func snapshotExternalGitStore(root, path string) (bool, error) {
	rootVolume, pathVolume := filepath.VolumeName(root), filepath.VolumeName(path)
	if runtime.GOOS == "windows" && rootVolume != "" && pathVolume != "" && !strings.EqualFold(rootVolume, pathVolume) {
		return true, nil
	}
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false, err
	}
	return !filepath.IsLocal(relative), nil
}
