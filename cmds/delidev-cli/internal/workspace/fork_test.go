package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkCopiesTwoDirtyRepositoriesAndPreservesSource(t *testing.T) {
	m := manager(t)
	first, second := repository(t), repository(t)
	request, _ := requestFor(first)
	request.Repositories = append(request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	var before []string
	for _, repo := range source.Repositories {
		if err := os.WriteFile(filepath.Join(repo.Path, "tracked.txt"), []byte("staged\n"), 0600); err != nil {
			t.Fatal(err)
		}
		gitTest(t, repo.Path, "add", "tracked.txt")
		for file, value := range map[string]string{"tracked.txt": "unstaged\n", "untracked.txt": "untracked\n", ".gitignore": "ignored.txt\n", "ignored.txt": "ignored\n"} {
			if err := os.WriteFile(filepath.Join(repo.Path, file), []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
		before = append(before, gitTest(t, repo.Path, "status", "--porcelain=v1", "--untracked-files=all"))
	}
	childRequest, err := m.ForkPreparation(context.Background(), source, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	child, err := m.Prepare(context.Background(), childRequest)
	if err != nil {
		t.Fatal(err)
	}
	if ValidateResult(childRequest, child, "darwin") != nil {
		t.Fatal("invalid published child")
	}
	for i, repo := range child.Repositories {
		if repo.Path == source.Repositories[i].Path || gitTest(t, repo.Path, "rev-parse", "--abbrev-ref", "HEAD") != "HEAD" {
			t.Fatal("shared or attached child")
		}
		if gitTest(t, repo.Path, "status", "--porcelain=v1", "--untracked-files=all") != before[i] || gitTest(t, source.Repositories[i].Path, "status", "--porcelain=v1", "--untracked-files=all") != before[i] {
			t.Fatal("dirty state changed")
		}
		bytes, err := os.ReadFile(filepath.Join(repo.Path, "ignored.txt"))
		if err != nil || string(bytes) != "ignored\n" {
			t.Fatal("ignored state lost")
		}
		if err := os.WriteFile(filepath.Join(repo.Path, "tracked.txt"), []byte("child only"), 0600); err != nil {
			t.Fatal(err)
		}
		original, _ := os.ReadFile(filepath.Join(source.Repositories[i].Path, "tracked.txt"))
		if string(original) != "unstaged\n" {
			t.Fatal("source modified by child")
		}
	}
}

func TestForkSecondRepositoryFailureDoesNotLeavePartialWorkspace(t *testing.T) {
	m := manager(t)
	first, second := repository(t), repository(t)
	request, _ := requestFor(first)
	request.Repositories = append(request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	source, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("tracked.txt", filepath.Join(source.Repositories[1].Path, "unsupported-link")); err != nil {
		t.Skip("symlink fixture unavailable")
	}
	childRequest, err := m.ForkPreparation(context.Background(), source, domain.NewID(), domain.Worktree)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Prepare(context.Background(), childRequest); err == nil {
		t.Fatal("unsupported second source was copied")
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(childRequest.SessionID))); !os.IsNotExist(err) {
		t.Fatal("partial owned child retained without uncertainty", err)
	}
	for _, repo := range source.Repositories {
		if _, err := os.Stat(filepath.Join(repo.Path, "tracked.txt")); err != nil {
			t.Fatal("source removed")
		}
	}
}
