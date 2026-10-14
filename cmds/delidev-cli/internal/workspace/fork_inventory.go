// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

// Reserve one allowance for all worktrees and their independently copied Git
// stores, including small bounded config/HEAD normalization overhead.
func newForkCopyBudget(repositories int) *snapshotCopyBudget {
	return &snapshotCopyBudget{bytes: maxForkBytes - uint64(4096*repositories), entries: maxForkEntries}
}

type forkGitInventory struct {
	common, admin                 string
	commonIdentity, adminIdentity string
	commonFiles, adminFiles       snapshotInventory
	// configProof is the frozen expected-config evidence for the child's retained
	// config files; see forkConfigProof.
	configProof forkConfigProof
}

func inspectForkGitInventory(ctx context.Context, git Git, source string, budget *snapshotCopyBudget) (forkGitInventory, error) {
	return inspectForkGitInventoryWithBudgets(ctx, git, source, budget, budget, true)
}

// inspectForkGitInventoryWithoutConfigProof rechecks the source inventory while
// retaining the config proof captured by the original snapshot. Re-parsing the
// same large config here would pin a second command buffer during preparation or
// final verification, defeating the aggregate Fork allowance.
func inspectForkGitInventoryWithoutConfigProof(ctx context.Context, git Git, source string, budget *snapshotCopyBudget) (forkGitInventory, error) {
	return inspectForkGitInventoryWithBudgets(ctx, git, source, budget, nil, false)
}

// inspectForkGitInventoryForPreparation keeps the source inventory traversal
// independently bounded while charging only the retained config proof against
// the request-wide budget. The copied Git files are charged by the later copy,
// so charging them again here would reject valid aggregate preparations.
func inspectForkGitInventoryForPreparation(ctx context.Context, git Git, source string, inventoryBudget, proofBudget *snapshotCopyBudget) (forkGitInventory, error) {
	return inspectForkGitInventoryWithBudgets(ctx, git, source, inventoryBudget, proofBudget, true)
}

func inspectForkGitInventoryWithBudgets(ctx context.Context, git Git, source string, inventoryBudget, proofBudget *snapshotCopyBudget, captureConfigProof bool) (forkGitInventory, error) {
	var result forkGitInventory
	git.readOnly, git.offline = true, true
	if err := validateSnapshotGitEntry(source); err != nil {
		return result, err
	}
	fields, err := git.revParseFields(ctx, source, 2, "--git-common-dir", "--absolute-git-dir")
	if err != nil {
		return result, err
	}
	result.common, result.admin = fields[0], fields[1]
	for _, p := range []string{result.common, result.admin} {
		canonical, err := filepath.EvalSymlinks(p)
		if err != nil || !filepath.IsAbs(p) || !sameNativePath(canonical, p) {
			return result, forkUnsupported()
		}
	}
	for _, name := range []string{"alternates", "http-alternates"} {
		if _, err = os.Lstat(filepath.Join(result.common, "objects", "info", name)); !errors.Is(err, os.ErrNotExist) {
			return result, forkUnsupported()
		}
	}
	if err = validateSnapshotGitConfig(ctx, git, source, result.admin); err != nil {
		return result, err
	}
	result.commonIdentity, err = directoryIdentityDigest(result.common)
	if err != nil {
		return result, err
	}
	result.adminIdentity, err = directoryIdentityDigest(result.admin)
	if err != nil {
		return result, err
	}
	// Common worktree registrations are external location pointers. Only this
	// selected worktree's complete private administration overlays the common store.
	result.commonFiles, err = walkSnapshotBudget(ctx, result.common, "", func(p string) bool { return p == "worktrees" || strings.HasPrefix(p, "worktrees/") }, maxForkEntries, inventoryBudget)
	if err != nil {
		return result, err
	}
	if err = validateGitInventory(result.commonFiles); err != nil {
		return result, err
	}
	if !sameNativePath(result.common, result.admin) {
		result.adminFiles, err = walkSnapshotBudget(ctx, result.admin, "", nil, maxForkEntries, inventoryBudget)
		if err != nil {
			return result, err
		}
		if err = validateGitInventory(result.adminFiles); err != nil {
			return result, err
		}
	}
	if captureConfigProof {
		if err = result.captureConfigProof(ctx, git, source, proofBudget); err != nil {
			return result, err
		}
	}
	return result, nil
}
func (i forkGitInventory) sourceMatches(other forkGitInventory) bool {
	return sameNativePath(i.common, other.common) && sameNativePath(i.admin, other.admin) && i.commonIdentity == other.commonIdentity && i.adminIdentity == other.adminIdentity && inventoryDigest(i.commonFiles) == inventoryDigest(other.commonFiles) && inventoryDigest(i.adminFiles) == inventoryDigest(other.adminFiles)
}
func forkGitRelocatableEntry(name string) bool {
	return name == "HEAD" || name == "config" || name == "config.worktree" || name == "commondir" || name == "gitdir" || name == "locked"
}
func (i forkGitInventory) copiedFiles() snapshotInventory {
	entries := map[string]snapshotEntry{}
	for _, e := range i.commonFiles.Entries {
		entries[e.Path] = e
	}
	for _, e := range i.adminFiles.Entries {
		entries[e.Path] = e
	}
	result := snapshotInventory{Entries: []snapshotEntry{}}
	for _, e := range entries {
		if !forkGitRelocatableEntry(e.Path) {
			result.Entries = append(result.Entries, e)
			result.Bytes += e.Size
		}
	}
	slices.SortFunc(result.Entries, func(a, b snapshotEntry) int { return strings.Compare(a.Path, b.Path) })
	return result
}

func (i forkGitInventory) recheckSource(ctx context.Context, git Git, source string) error {
	current, err := inspectForkGitInventoryWithoutConfigProof(ctx, git, source, newForkCopyBudget(1))
	if err != nil {
		// This is a recheck of an already admitted source. A changed source
		// that now exceeds a resource bound is snapshot drift, not a fresh
		// admission failure.
		if domain.SafeError(err).Code == domain.ResourceExhausted {
			return forkSnapshotChanged()
		}
		return err
	}
	if !i.sourceMatches(current) {
		return forkSnapshotChanged()
	}
	return nil
}

func (i forkGitInventory) verify(ctx context.Context, git Git, source, target string) error {
	if err := i.recheckSource(ctx, git, source); err != nil {
		return err
	}
	targetBudget := newForkCopyBudget(1)
	targetFiles, err := walkSnapshotBudget(ctx, filepath.Join(target, ".git"), "", nil, maxForkEntries, targetBudget)
	if err != nil {
		return err
	}
	if err = validateGitInventory(targetFiles); err != nil {
		return err
	}
	child := forkGitInventory{commonFiles: targetFiles}
	if inventoryDigest(i.copiedFiles()) != inventoryDigest(child.copiedFiles()) {
		return forkSnapshotChanged()
	}
	fields, err := git.revParseFields(ctx, target, 2, "--git-common-dir", "--absolute-git-dir")
	expected := filepath.Join(target, ".git")
	if err != nil || !sameNativePath(fields[0], expected) || !sameNativePath(fields[1], expected) {
		return forkSnapshotChanged()
	}
	roots, err := git.revParseFields(ctx, target, 1, "--show-toplevel")
	if err != nil || !sameNativePath(roots[0], target) {
		return forkSnapshotChanged()
	}
	if err = validateSnapshotGitConfig(ctx, git, target, expected); err != nil {
		return err
	}
	// The copied config files were excluded from the inventory comparison above
	// because normalization rewrites them. Prove them against the frozen source.
	if err = i.verifyChildConfig(ctx, git, target, targetBudget); err != nil {
		return err
	}
	if _, err = os.Lstat(filepath.Join(expected, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		return forkUnsupported()
	}
	return nil
}

// syncForkConfigFile flushes one rewritten child config file to stable storage.
func syncForkConfigFile(path string) error {
	file, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	err = file.Sync()
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	return err
}

func (m *Manager) prepareForkGitInventory(ctx context.Context, git Git, spec RepositorySpec, manifest *Manifest, write func() error, budget *snapshotCopyBudget, snapshot *ForkSnapshot) (PreparedRepository, *forkCopy, error) {
	entry := &manifest.Repositories[len(manifest.Repositories)-1]
	git.readOnly, git.offline = true, true
	marker, err := captureForkRootMarker(spec.Checkout)
	if err != nil {
		return *entry, nil, err
	}
	if pinned := snapshot.sourceMarker(spec.Checkout); pinned != nil {
		marker = *pinned
	}
	var proof forkGitInventory
	if snapshot != nil {
		for _, copy := range snapshot.copies {
			if copy.source == spec.Checkout && copy.gitInventory != nil {
				proof = *copy.gitInventory
				if err := proof.recheckSource(ctx, git, spec.Checkout); err != nil {
					return *entry, nil, err
				}
				break
			}
		}
	}
	if !proof.configProof.bound {
		var err error
		proof, err = inspectForkGitInventoryForPreparation(ctx, git, spec.Checkout, newForkCopyBudget(1), budget)
		if err != nil {
			return *entry, nil, err
		}
	}
	proof.bindRemote(spec)
	if spec.Starting.Type != domain.CommitReference || spec.Base.Type != domain.CommitReference || spec.Starting.Name != spec.Base.Name {
		return *entry, nil, forkUnsupported()
	}
	entry.StartingCommit, entry.BaseCommit = spec.Starting.Name, spec.Base.Name
	// Journal the empty independently owned Git directory before writing payloads.
	// Failure retains the same inode commitment for ordinary original cleanup.
	if err = security.CreatePrivateDirExclusive(filepath.Join(entry.Path, ".git")); err != nil {
		return *entry, nil, err
	}
	entry.CloneIdentityDigest, err = independentDirectoryDigest(*entry)
	if err != nil || write() != nil {
		return *entry, nil, ResultUncertain()
	}
	if err = m.copySnapshotGitBudget(ctx, manifest.SessionID, PreparedRepository{ID: spec.ID, Path: spec.Checkout, BaseCommit: entry.BaseCommit, StartingCommit: entry.StartingCommit}, entry.Path, budget, true); err != nil {
		return *entry, nil, err
	}
	// Preserve every original reflog byte. A detached child HEAD is a private
	// publication seed, not a new event in the source's copied native history.
	if err = security.WriteAtomic(filepath.Join(entry.Path, ".git", "HEAD"), []byte(entry.StartingCommit+"\n")); err != nil {
		return *entry, nil, err
	}
	remote := forkRemoteName(spec)
	if _, err = git.run(ctx, entry.Path, "config", "--local", forkRemoteURLKey(remote), spec.RemoteURL); err != nil {
		return *entry, nil, err
	}
	// With extensions.worktreeConfig, Git reads config.worktree after config, so a
	// retained worktree-scoped URL for the selected remote would keep the stale
	// source endpoint effective. Drop it so the child has one selected URL.
	worktreeConfigRewritten := proof.configProof.worktree.hasKey(forkRemoteURLKey(remote))
	if worktreeConfigRewritten {
		if _, err = git.run(ctx, entry.Path, "config", "--file", filepath.Join(entry.Path, ".git", "config.worktree"), "--unset-all", forkRemoteURLKey(remote)); err != nil {
			return *entry, nil, err
		}
		m.Logger.InfoContext(ctx, "workspace_fork_git_inventory_worktree_remote_url_removed", "session_id", manifest.SessionID, "repository_id", spec.ID, "remote", remote)
	}
	digest, err := scanForkTreePinned(ctx, spec.Checkout, entry.Path, true, maxForkEntries, &marker, budget)
	if err != nil {
		return *entry, nil, err
	}
	_, index, err := forkIndex(ctx, git, spec.Checkout)
	if err != nil {
		return *entry, nil, err
	}
	hash := sha256.Sum256(index)
	copy := forkCopy{sourceMarker: &marker, source: spec.Checkout, target: entry.Path, tree: digest, git: true, head: entry.StartingCommit, index: hex.EncodeToString(hash[:]), entryLimit: maxForkEntries, gitInventory: &proof}
	if err = copy.verify(ctx, git); err != nil {
		m.Logger.WarnContext(ctx, "workspace_fork_git_inventory_verify_failed", "session_id", manifest.SessionID, "repository_id", spec.ID, "phase", "copied_git_config_and_inventory", "code", domain.SafeError(err).Code)
		return *entry, nil, err
	}
	if err = verifyIndependentDirectory(*entry, true); err != nil {
		return *entry, nil, err
	}
	// Config is the only rewritten payload besides HEAD, and must reach stable
	// storage before the Ready publication can authorize this child lifetime.
	if err = syncForkConfigFile(filepath.Join(entry.Path, ".git", "config")); err != nil {
		return *entry, nil, err
	}
	if worktreeConfigRewritten {
		// The --unset-all rewrite replaced config.worktree after the copy budget
		// synced it, so a crash could otherwise restore the stale source URL.
		if err = syncForkConfigFile(filepath.Join(entry.Path, ".git", "config.worktree")); err != nil {
			return *entry, nil, err
		}
	}
	if err = syncSnapshotDir(filepath.Join(entry.Path, ".git")); err != nil {
		return *entry, nil, err
	}
	m.Logger.InfoContext(ctx, "workspace_fork_git_inventory_verified", "session_id", manifest.SessionID, "repository_id", spec.ID, "git_entries", len(proof.commonFiles.Entries)+len(proof.adminFiles.Entries))
	startupProgress(ctx, domain.StartupWorkspaceClone, domain.StartupProgressCompleted)
	startupProgress(ctx, domain.StartupWorkspaceCheckout, domain.StartupProgressCompleted)
	return *entry, &copy, nil
}
