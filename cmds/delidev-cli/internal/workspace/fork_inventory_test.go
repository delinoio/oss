// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func forkInventoryCopyFixture(t *testing.T, linked bool) (*Manager, string, Manifest, forkCopy, string) {
	t.Helper()
	source, canonicalErr := filepath.EvalSymlinks(repository(t))
	if canonicalErr != nil {
		t.Fatal(canonicalErr)
	}
	selected := source
	head := gitTest(t, source, "rev-parse", "HEAD")
	if linked {
		base, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		selected = filepath.Join(base, "selected")
		gitTest(t, source, "worktree", "add", "--detach", selected, head)
	}
	if err := os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("reflog-only history\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, source, "add", "tracked.txt")
	gitTest(t, source, "commit", "-m", "reflog-only history")
	lost := gitTest(t, source, "rev-parse", "HEAD")
	gitTest(t, source, "reset", "--hard", head)
	gitTest(t, source, "update-ref", "refs/archive/unpushed", head)
	if err := os.WriteFile(filepath.Join(selected, "tracked.txt"), []byte("staged dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, selected, "add", "tracked.txt")
	if err := os.WriteFile(filepath.Join(selected, "tracked.txt"), []byte("unstaged dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(selected, "untracked.txt"), []byte("untracked dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	if err := m.initialize(); err != nil {
		t.Fatal(err)
	}
	session, repo := domain.NewID(), domain.NewID()
	target := filepath.Join(m.Root, "inventory-unit", string(repo))
	if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
		t.Fatal(err)
	}
	if err := security.CreatePrivateDirExclusive(target); err != nil {
		t.Fatal(err)
	}
	rootDigest, err := directoryIdentityDigest(target)
	if err != nil {
		t.Fatal(err)
	}
	manifest := Manifest{SessionID: session, Repositories: []PreparedRepository{{ID: repo, SourceKind: IndependentForkSource, Source: target, Path: target, Owned: true, CloneRootDigest: rootDigest}}, PrimaryPath: target}
	spec := RepositorySpec{ID: repo, SourceKind: IndependentForkSource, Checkout: selected, RemoteURL: "https://github.com/fixture/repo.git", Starting: domain.Reference{Type: domain.CommitReference, Name: head}, Base: domain.Reference{Type: domain.CommitReference, Name: head}}
	git := m.Git
	git.OwnerID = session
	prepared, copy, err := m.prepareForkGitInventory(context.Background(), git, spec, &manifest, func() error { return nil }, newForkCopyBudget(1), nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest.Repositories[0] = prepared
	manifest.State = Ready
	return m, selected, manifest, *copy, lost
}
func TestIndependentForkInventoryCommonReflogAndObjects(t *testing.T) {
	_, source, child, proof, lost := forkInventoryCopyFixture(t, false)
	target := child.PrimaryPath
	if strings.Contains(gitTest(t, source, "rev-list", "--all"), lost) {
		t.Fatal("lost commit fixture is reachable from a ref")
	}
	for _, args := range [][]string{{"for-each-ref", "--format=%(refname) %(objectname)"}, {"reflog", "show", "--format=%H", "main"}, {"reflog", "show", "--format=%H", "HEAD"}, {"status", "--porcelain=v1", "--untracked-files=all"}} {
		if gitTest(t, source, args...) != gitTest(t, target, args...) {
			t.Fatalf("source inventory changed: %v", args)
		}
	}
	object := filepath.Join(proof.gitInventory.common, "objects", lost[:2], lost[2:])
	sourceInfo, err := os.Stat(object)
	if err != nil {
		t.Fatal(err)
	}
	childInfo, err := os.Stat(filepath.Join(target, ".git", "objects", lost[:2], lost[2:]))
	if err != nil || os.SameFile(sourceInfo, childInfo) {
		t.Fatal("object missing or hardlinked")
	}
	if _, err = os.Lstat(filepath.Join(target, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatal("child borrows an object store")
	}
	if err = os.Rename(source, source+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(source+"-offline", source)
	if gitTest(t, target, "show", lost+":tracked.txt") != "reflog-only history" || !strings.Contains(gitTest(t, target, "reflog", "show", "--format=%H", "main"), lost) {
		t.Fatal("parent removal lost independent native history")
	}
}
func TestIndependentForkInventoryLinkedAdministration(t *testing.T) {
	_, source, child, proof, lost := forkInventoryCopyFixture(t, true)
	target := child.PrimaryPath
	for _, args := range [][]string{{"for-each-ref", "--format=%(refname) %(objectname)"}, {"reflog", "show", "--format=%H", "main"}, {"reflog", "show", "--format=%H", "HEAD"}, {"status", "--porcelain=v1", "--untracked-files=all"}} {
		if gitTest(t, source, args...) != gitTest(t, target, args...) {
			t.Fatalf("selected linked administration lost: %v", args)
		}
	}
	for _, pointer := range []string{"commondir", "gitdir", "worktrees"} {
		if _, err := os.Lstat(filepath.Join(target, ".git", pointer)); !os.IsNotExist(err) {
			t.Fatalf("child retains %s pointer", pointer)
		}
	}
	common := proof.gitInventory.common
	if err := os.Rename(common, common+"-offline"); err != nil {
		t.Fatal(err)
	}
	defer os.Rename(common+"-offline", common)
	if gitTest(t, target, "show", lost+":tracked.txt") != "reflog-only history" || !strings.Contains(gitTest(t, target, "reflog", "show", "--format=%H", "main"), lost) {
		t.Fatal("child relies on original common or admin store")
	}
}
func TestIndependentForkInventorySourceDriftAndChildTampering(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixture(t, false)
	git := m.Git
	git.OwnerID = child.SessionID
	// Ref, reflog-only bytes and unreferenced objects are independently pinned,
	// even though the selected source HEAD/index/worktree remain unchanged.
	files := []string{"refs/archive/unpushed", "logs/HEAD", "objects/info/fork-inventory-fixture"}
	for _, relative := range files {
		path := filepath.Join(proof.gitInventory.common, filepath.FromSlash(relative))
		original, readErr := os.ReadFile(path)
		if readErr != nil && !os.IsNotExist(readErr) {
			t.Fatal(readErr)
		}
		if err := os.WriteFile(path, append(append([]byte{}, original...), []byte("changed inventory\n")...), 0600); err != nil {
			t.Fatal(err)
		}
		if err := proof.verify(context.Background(), git); err == nil {
			t.Fatalf("source %s drift accepted", relative)
		}
		if os.IsNotExist(readErr) {
			os.Remove(path)
		} else {
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := proof.verify(context.Background(), git); err != nil {
		t.Fatal("restored source proof failed", err)
	}
	path := filepath.Join(child.PrimaryPath, ".git", "logs", "HEAD")
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, append(original, []byte("child tampering\n")...), 0600); err != nil {
		t.Fatal(err)
	}
	if err = proof.verify(context.Background(), git); err == nil {
		t.Fatal("incomplete child history accepted")
	}
	if err = os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(child.PrimaryPath, ".git", "HEAD"), []byte("ref: refs/archive/unpushed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	// A rewritten worktree path would borrow the original dirty files despite
	// owning all object files. The publication guard must reject it.
	gitTest(t, child.PrimaryPath, "config", "core.worktree", source)
	if err = proof.verify(context.Background(), git); err == nil {
		t.Fatal("child borrowed original worktree")
	}
	if gitTest(t, source, "rev-parse", "HEAD") != proof.head {
		t.Fatal("verification moved source HEAD")
	}
}
func TestIndependentForkInventoryAggregateBudgetAndAlternates(t *testing.T) {
	m, source, child, proof, _ := forkInventoryCopyFixture(t, false)
	git := m.Git
	git.OwnerID = child.SessionID
	budget := &snapshotCopyBudget{bytes: 1, entries: 2}
	if _, err := inspectForkGitInventory(context.Background(), git, source, budget); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatalf("unbounded Git inventory: %v", err)
	}
	gitBytes := proof.gitInventory.commonFiles.Bytes
	budget = &snapshotCopyBudget{bytes: gitBytes, entries: maxForkEntries}
	if _, err := scanForkTreePinned(context.Background(), source, "", true, maxForkEntries, proof.sourceMarker, budget); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectForkGitInventory(context.Background(), git, source, budget); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("worktree and Git store got separate byte allowances", err)
	}
	alternate := filepath.Join(proof.gitInventory.common, "objects", "info", "alternates")
	if err := os.WriteFile(alternate, []byte(filepath.Join(t.TempDir(), "borrowed")), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectForkGitInventory(context.Background(), git, source, newForkCopyBudget(1)); err == nil {
		t.Fatal("alternate source dependency admitted")
	}
}

func TestIndependentForkInventoryRejectsForeignGitRoot(t *testing.T) {
	m, source, child, _, _ := forkInventoryCopyFixture(t, false)
	target, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = security.CreatePrivateDirExclusive(filepath.Join(target, ".git")); err != nil {
		t.Fatal(err)
	}
	m.storageCopyFault = func(path string) error {
		if err := os.Rename(filepath.Join(path, ".git"), filepath.Join(path, "original-owned-git")); err != nil {
			return err
		}
		if err := security.CreatePrivateDirExclusive(filepath.Join(path, ".git")); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(path, ".git", "foreign-marker"), []byte("foreign ownership"), 0600)
	}
	err = m.copySnapshotGitBudget(context.Background(), child.SessionID, PreparedRepository{Path: source}, target, newForkCopyBudget(1), true)
	if err == nil {
		t.Fatal("replaced Git directory accepted")
	}
	files, readErr := os.ReadDir(filepath.Join(target, ".git"))
	if readErr != nil || len(files) != 1 || files[0].Name() != "foreign-marker" {
		t.Fatal("foreign directory was changed", readErr)
	}
	marker, readErr := os.ReadFile(filepath.Join(target, ".git", "foreign-marker"))
	if readErr != nil || string(marker) != "foreign ownership" {
		t.Fatal("foreign content was changed", readErr)
	}
}
