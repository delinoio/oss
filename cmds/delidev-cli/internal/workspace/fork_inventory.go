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

// forkVerifyByteLimit is the aggregate byte bound that child verification shares
// with every live frozen source config proof. It is a variable only so tests can
// exercise exact boundaries without allocating hundreds of mebibytes; production
// code never assigns it.
var forkVerifyByteLimit uint64 = maxForkBytes

// newForkVerifyBudget starts child verification with the single-repository
// reserve and deducts the retained config proofs of every live Fork source, not
// only the repository being verified. Those proofs stay allocated until the Fork
// snapshot is dropped, so child config layers may only use what remains.
func newForkVerifyBudget(liveProofBytes uint64) (*snapshotCopyBudget, error) {
	budget := &snapshotCopyBudget{bytes: forkVerifyByteLimit - 4096, entries: maxForkEntries}
	if liveProofBytes > budget.bytes {
		return nil, forkSnapshotChanged()
	}
	budget.bytes -= liveProofBytes
	return budget, nil
}

// forkLiveConfigProofBytes sums the retained config proof bytes of every
// independent Fork source that is still live. Groups are ordered from the most
// authoritative holder (the snapshot) to later holders (prepared copies). A copy
// whose source checkout appeared in an earlier group reuses that group's parsed
// proof buffers, so only the buffer and record-array backing is deduplicated; the
// reusing copy still owns its own []string slice headers, which are charged.
// Copies within one group are always counted separately.
//
// The total is also admitted against the verification allowance here, so every
// verification phase (per-copy preparation, the final all-copy loop and
// ForkSnapshot.Verify) rejects an over-budget total with a snapshot conflict
// before it touches any child Git layer.
func forkLiveConfigProofBytes(groups ...[]forkCopy) (uint64, error) {
	var total uint64
	seen := map[string]bool{}
	for _, group := range groups {
		var added []string
		for _, copy := range group {
			if copy.gitInventory == nil {
				continue
			}
			charge := copy.gitInventory.configProof.retainedBytes
			if seen[copy.source] {
				charge = min(charge, copy.gitInventory.configProof.sliceHeaderBytes())
			} else {
				added = append(added, copy.source)
			}
			if charge > ^uint64(0)-total {
				return 0, forkSnapshotChanged()
			}
			total += charge
		}
		for _, source := range added {
			seen[source] = true
		}
	}
	if _, err := newForkVerifyBudget(total); err != nil {
		return 0, err
	}
	return total, nil
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

// admitLiveProofs deducts every live frozen source proof from the verification
// allowance. liveProofBytes is the retained config proof total of every live Fork
// source including this one. It runs before any source or child Git command so an
// already over-budget verification reports a snapshot conflict instead of whatever
// the child layers would have produced.
func (i forkGitInventory) admitLiveProofs(liveProofBytes uint64) (*snapshotCopyBudget, error) {
	if liveProofBytes < i.configProof.retainedBytes {
		return nil, forkSnapshotChanged()
	}
	return newForkVerifyBudget(liveProofBytes)
}

// verify proves the copied Git directory against the frozen source. The child Git
// files and config layers are charged to the allowance that remains after every
// live proof, and that allowance is established before any Git command runs.
func (i forkGitInventory) verify(ctx context.Context, git Git, source, target string, liveProofBytes uint64) error {
	targetBudget, err := i.admitLiveProofs(liveProofBytes)
	if err != nil {
		return err
	}
	if err = i.recheckSource(ctx, git, source); err != nil {
		return err
	}
	// The remaining allowance already excludes the live proofs, so a child Git
	// directory that no longer fits beside them is drift, not a fresh admission.
	targetFiles, err := walkSnapshotBudget(ctx, filepath.Join(target, ".git"), "", nil, maxForkEntries, targetBudget)
	if err != nil {
		return forkChildConfigError(err)
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

func (m *Manager) prepareForkGitInventory(ctx context.Context, git Git, spec RepositorySpec, manifest *Manifest, write func() error, budget *snapshotCopyBudget, snapshot *ForkSnapshot, prior []forkCopy) (PreparedRepository, *forkCopy, error) {
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
	// Earlier prepared repositories and the snapshot still hold their frozen
	// source proofs while this child is copied and verified. Admit the total
	// before the child is created, so an over-budget Fork never writes or parses a
	// child Git layer.
	var snapshotCopies []forkCopy
	if snapshot != nil {
		snapshotCopies = snapshot.copies
	}
	liveProofBytes, err := forkLiveConfigProofBytes(snapshotCopies, prior, []forkCopy{{source: spec.Checkout, gitInventory: &proof}})
	if err != nil {
		m.Logger.WarnContext(ctx, "workspace_fork_git_inventory_verify_failed", "session_id", manifest.SessionID, "repository_id", spec.ID, "phase", "live_proof_admission", "code", domain.SafeError(err).Code)
		return *entry, nil, err
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
	if err = copy.verify(ctx, git, liveProofBytes); err != nil {
		m.Logger.WarnContext(ctx, "workspace_fork_git_inventory_verify_failed", "session_id", manifest.SessionID, "repository_id", spec.ID, "phase", "copied_git_config_and_inventory", "live_proof_bytes", liveProofBytes, "code", domain.SafeError(err).Code)
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
