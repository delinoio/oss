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
}

// Git reports Windows absolute paths with its own separators and casing. Keep
// the filesystem's canonical spelling only after proving the original directory
// identity; strict digest checks must not accept links or adopt a replacement.
func canonicalForkGitDirectory(path string) (string, error) {
	named, err := os.Lstat(path)
	if err != nil || !filepath.IsAbs(path) || !named.IsDir() || named.Mode()&os.ModeSymlink != 0 {
		return "", forkUnsupported()
	}
	original, err := security.StableStat(path)
	if err != nil {
		return "", forkUnsupported()
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !sameNativePath(canonical, path) {
		return "", forkUnsupported()
	}
	resolved, err := security.StableStat(canonical)
	if err != nil || !os.SameFile(original, resolved) {
		return "", forkUnsupported()
	}
	return canonical, nil
}

func inspectForkGitInventory(ctx context.Context, git Git, source string, budget *snapshotCopyBudget) (forkGitInventory, error) {
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
	for i, p := range []string{result.common, result.admin} {
		canonical, err := canonicalForkGitDirectory(p)
		if err != nil {
			return result, err
		}
		if i == 0 {
			result.common = canonical
		} else {
			result.admin = canonical
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
	result.commonFiles, err = walkSnapshotBudget(ctx, result.common, "", func(p string) bool { return p == "worktrees" || strings.HasPrefix(p, "worktrees/") }, maxForkEntries, budget)
	if err != nil {
		return result, err
	}
	if err = validateGitInventory(result.commonFiles); err != nil {
		return result, err
	}
	if !sameNativePath(result.common, result.admin) {
		result.adminFiles, err = walkSnapshotBudget(ctx, result.admin, "", nil, maxForkEntries, budget)
		if err != nil {
			return result, err
		}
		if err = validateGitInventory(result.adminFiles); err != nil {
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
func (i forkGitInventory) verify(ctx context.Context, git Git, source, target string) error {
	current, err := inspectForkGitInventory(ctx, git, source, newForkCopyBudget(1))
	if err != nil {
		return err
	}
	if !i.sourceMatches(current) {
		return forkSnapshotChanged()
	}
	targetFiles, err := walkSnapshotBudget(ctx, filepath.Join(target, ".git"), "", nil, maxForkEntries, newForkCopyBudget(1))
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
	if _, err = os.Lstat(filepath.Join(expected, "objects", "info", "alternates")); !errors.Is(err, os.ErrNotExist) {
		return forkUnsupported()
	}
	return nil
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
	proof, err := inspectForkGitInventory(ctx, git, spec.Checkout, newForkCopyBudget(1))
	if err != nil {
		return *entry, nil, err
	}
	if snapshot != nil {
		for _, copy := range snapshot.copies {
			if copy.source == spec.Checkout && copy.gitInventory != nil && !copy.gitInventory.sourceMatches(proof) {
				return *entry, nil, forkSnapshotChanged()
			}
		}
	}
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
	remote := spec.PreferredRemote
	if remote == "" {
		remote = "origin"
	}
	if _, err = git.run(ctx, entry.Path, "config", "--local", "remote."+remote+".url", spec.RemoteURL); err != nil {
		return *entry, nil, err
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
		return *entry, nil, err
	}
	if err = verifyIndependentDirectory(*entry, true); err != nil {
		return *entry, nil, err
	}
	// Config is the only rewritten payload besides HEAD, and must reach stable
	// storage before the Ready publication can authorize this child lifetime.
	config, err := os.OpenFile(filepath.Join(entry.Path, ".git", "config"), os.O_RDWR, 0)
	if err != nil {
		return *entry, nil, err
	}
	err = config.Sync()
	closeErr := config.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return *entry, nil, err
	}
	if err = syncSnapshotDir(filepath.Join(entry.Path, ".git")); err != nil {
		return *entry, nil, err
	}
	m.Logger.InfoContext(ctx, "workspace_fork_git_inventory_verified", "session_id", manifest.SessionID, "repository_id", spec.ID, "git_entries", len(proof.commonFiles.Entries)+len(proof.adminFiles.Entries))
	startupProgress(ctx, domain.StartupWorkspaceClone, domain.StartupProgressCompleted)
	startupProgress(ctx, domain.StartupWorkspaceCheckout, domain.StartupProgressCompleted)
	return *entry, &copy, nil
}
