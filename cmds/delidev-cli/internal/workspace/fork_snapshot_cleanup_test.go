// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkSnapshotDriftBeforeNativeCreationRollsBackOwnedCopies(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the deterministic Git operation barrier uses a POSIX shell")
	}
	for _, point := range []string{"before-first-copy", "after-first-copy"} {
		for _, change := range []string{"file", "head", "index"} {
			t.Run(point+"/"+change, func(t *testing.T) { forkSnapshotDriftFixture(t, point, change, false) })
		}
	}
	t.Run("cleanup-failure", func(t *testing.T) { forkSnapshotDriftFixture(t, "after-first-copy", "file", true) })
}

func forkSnapshotDriftFixture(t *testing.T, point, change string, failCleanup bool) {
	t.Helper()
	m := manager(t)
	request, _ := requestFor(repository(t))
	request.Repositories = append(request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: repository(t), Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	first := source.Repositories[0].Path
	oldHead := gitTest(t, first, "rev-parse", "HEAD")
	gitTest(t, first, "-c", "core.hooksPath="+m.Git.HooksDir, "commit", "--allow-empty", "-m", "snapshot drift target")
	nextHead := gitTest(t, first, "rev-parse", "HEAD")
	gitTest(t, first, "update-ref", "--no-deref", "HEAD", oldHead)
	childRequest, err := m.ForkPreparation(context.Background(), source, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.InspectForkSnapshot(context.Background(), source, childRequest)
	if err != nil {
		t.Fatal(err)
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	trigger := source.Repositories[0].Path
	if point == "after-first-copy" {
		trigger = source.Repositories[1].Path
	}
	mutation := "printf 'source edit\\n' > " + fixtureShellQuote(filepath.Join(first, "tracked.txt"))
	if change == "head" {
		mutation = fixtureShellQuote(git) + " -C " + fixtureShellQuote(first) + " update-ref --no-deref HEAD " + fixtureShellQuote(nextHead)
	} else if change == "index" {
		mutation = fixtureShellQuote(git) + " -C " + fixtureShellQuote(first) + " update-index --assume-unchanged tracked.txt"
	}
	cleanup := ""
	if failCleanup {
		cleanup = "if [ \"$operation\" = remove ]; then exit 128; fi"
	}
	// Delegate every command to real Git. Mutate the first source only at the
	// deterministic worktree-add boundary, without timing or filesystem races.
	script := fmt.Sprintf(`#!/bin/sh
operation=none
previous=none
for arg do
  if [ "$previous" = worktree ]; then operation="$arg"; fi
  previous="$arg"
done
%s
if [ "$2" = %s ] && [ "$operation" = add ]; then
  %s "$@" || exit $?
  %s || exit $?
  exit 0
fi
exec %s "$@"
`, cleanup, fixtureShellQuote(trigger), fixtureShellQuote(git), mutation, fixtureShellQuote(git))
	m.Git.Executable = filepath.Join(t.TempDir(), "snapshot-git")
	if err := os.WriteFile(m.Git.Executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	_, err = m.PrepareFork(context.Background(), childRequest, snapshot)
	code := domain.SafeError(err).Code
	childRoot := filepath.Join(m.Root, "workspaces", string(childRequest.SessionID))
	if failCleanup {
		retained, readErr := m.Read(childRequest.SessionID)
		if code != domain.RecoveryRequired || readErr != nil || retained.State != CleanupPending {
			t.Fatal("failed cleanup lost uncertainty or its ownership journal", err, readErr)
		}
	} else {
		if code != domain.Conflict {
			t.Fatal("pre-native drift did not settle as a definite failed copy", err)
		}
		if _, err := os.Lstat(childRoot); !os.IsNotExist(err) {
			t.Fatal("owned child copy remains after verified cleanup", err)
		}
		for _, repo := range source.Repositories {
			registered := gitTest(t, repo.Path, "worktree", "list", "--porcelain")
			if strings.Contains(registered, childRoot) || strings.Count(registered, "worktree ") != 2 {
				t.Fatal("child Git registration remains or source registration was removed")
			}
		}
	}
	for _, repo := range source.Repositories {
		if _, err := os.Stat(filepath.Join(repo.Path, "tracked.txt")); err != nil {
			t.Fatal("rollback removed an original source", err)
		}
	}
	switch change {
	case "file":
		raw, _ := os.ReadFile(filepath.Join(first, "tracked.txt"))
		if string(raw) != "source edit\n" {
			t.Fatal("rollback changed the independent source edit")
		}
	case "head":
		if gitTest(t, first, "rev-parse", "HEAD") != nextHead {
			t.Fatal("rollback changed the independent source HEAD")
		}
	case "index":
		if !strings.HasPrefix(gitTest(t, first, "ls-files", "-v", "tracked.txt"), "h ") {
			t.Fatal("rollback changed the independent source index")
		}
	}
}
