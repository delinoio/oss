package workspace

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func gitTest(t *testing.T, root string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", root, "-c", "user.name=DeliDev Test", "-c", "user.email=delidev-test@example.invalid", "-c", "commit.gpgSign=false"}, args...)...)
	command.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}
func repository(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	gitTest(t, root, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "initial")
	return root
}
func manager(t *testing.T) *Manager {
	t.Helper()
	return &Manager{Root: filepath.Join(t.TempDir(), "worker"), Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
}
func requestFor(root string) (PrepareRequest, domain.ID) {
	machine, repo := domain.NewID(), domain.NewID()
	return PrepareRequest{SessionID: domain.NewID(), MachineID: machine, OriginMachineID: machine, Type: domain.Worktree, PrimaryRepository: repo, Repositories: []RepositorySpec{{ID: repo, Checkout: root, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}}}}, repo
}
func TestInspectRootSubdirectoryLinkedWorktreeWithoutURLs(t *testing.T) {
	root := repository(t)
	if err := os.Mkdir(filepath.Join(root, "sub"), 0700); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "remote", "add", "origin", "https://user:seed-secret@example.invalid/repo.git")
	g := Git{}
	inspection, err := g.Inspect(context.Background(), filepath.Join(root, "sub"))
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if inspection.Root != canonical || len(inspection.Remotes) != 1 || inspection.Remotes[0] != "origin" {
		t.Fatalf("bad inspection: %+v", inspection)
	}
	linked := filepath.Join(t.TempDir(), "linked")
	gitTest(t, root, "worktree", "add", "--detach", linked, "HEAD")
	inspection, err = g.Inspect(context.Background(), linked)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(inspection.Root) != "linked" {
		t.Fatal("linked root lost", inspection)
	}
}
func TestDetachedAllRepositoryPreparationAndIdempotency(t *testing.T) {
	root, second := repository(t), repository(t)
	m := manager(t)
	request, repo := requestFor(root)
	other := domain.NewID()
	request.Repositories = append(request.Repositories, RepositorySpec{ID: other, Checkout: second, Starting: domain.Reference{Type: domain.LocalBranch, Name: "main"}})
	request.PrimaryRepository = other
	before := gitTest(t, root, "rev-parse", "HEAD")
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.State != Ready || len(manifest.Repositories) != 2 || manifest.PrimaryPath != manifest.Repositories[1].Path {
		t.Fatalf("incomplete workspace: %+v", manifest)
	}
	if got := gitTest(t, manifest.Repositories[0].Path, "rev-parse", "HEAD"); got != before {
		t.Fatal("wrong creation commit")
	}
	if got := gitTest(t, manifest.Repositories[0].Path, "rev-parse", "--abbrev-ref", "HEAD"); got != "HEAD" {
		t.Fatal("worktree is not detached", got)
	}
	again, err := m.Prepare(context.Background(), request)
	if err != nil || again.PrimaryPath != manifest.PrimaryPath {
		t.Fatalf("duplicate preparation: %+v %v", again, err)
	}
	request.Repositories[0].Starting.Name = "another"
	if _, err := m.Prepare(context.Background(), request); err == nil {
		t.Fatal("changed request overwrote workspace")
	}
	if gitTest(t, root, "rev-parse", "HEAD") != before || gitTest(t, root, "branch", "--show-current") != "main" {
		t.Fatal("original checkout changed")
	}
	if manifest.Repositories[0].ID != repo {
		t.Fatal("repository identity changed")
	}
}
func TestFailureRollsBackOnlyNewWorktrees(t *testing.T) {
	root := repository(t)
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("preserve"), 0600); err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	request, _ := requestFor(root)
	second := repository(t)
	request.Repositories = append(request.Repositories, RepositorySpec{ID: domain.NewID(), Checkout: second, Starting: domain.Reference{Type: domain.LocalBranch, Name: "absent"}})
	if _, err := m.Prepare(context.Background(), request); err == nil {
		t.Fatal("partial project accepted")
	}
	if _, err := os.Stat(filepath.Join(m.Root, "workspaces", string(request.SessionID))); !os.IsNotExist(err) {
		t.Fatalf("failed attempt not removed: %v", err)
	}
	if value, err := os.ReadFile(filepath.Join(root, "untracked.txt")); err != nil || string(value) != "preserve" {
		t.Fatal("original files removed")
	}
	if worktrees := gitTest(t, root, "worktree", "list", "--porcelain"); strings.Count(worktrees, "worktree ") != 1 {
		t.Fatal("partial worktree registration remains", worktrees)
	}
}
func TestLocalUsesCurrentCheckoutWithoutFetchOrStartingSelection(t *testing.T) {
	root := repository(t)
	gitTest(t, root, "checkout", "-b", "existing")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("dirty\n"), 0600); err != nil {
		t.Fatal(err)
	}
	m := manager(t)
	request, _ := requestFor(root)
	request.Type = domain.Local
	request.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "missing", Name: "not-a-branch"}
	request.Repositories[0].AutoFetch = true
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	canonical, _ := filepath.EvalSymlinks(root)
	if manifest.PrimaryPath != canonical || manifest.Repositories[0].Owned {
		t.Fatal("Local ownership changed")
	}
	if gitTest(t, root, "branch", "--show-current") != "existing" {
		t.Fatal("Local branch changed")
	}
	if value, _ := os.ReadFile(filepath.Join(root, "tracked.txt")); string(value) != "dirty\n" {
		t.Fatal("Local edits changed")
	}
	request.SessionID = domain.NewID()
	request.OriginMachineID = domain.NewID()
	if _, err := m.Prepare(context.Background(), request); err == nil {
		t.Fatal("remote Local permitted")
	}
}
func TestRemoteFetchUsesUpdatedCommitAndNeverStaleFallback(t *testing.T) {
	upstream := repository(t)
	clone := filepath.Join(t.TempDir(), "clone")
	gitTest(t, filepath.Dir(clone), "clone", upstream, clone)
	if err := os.WriteFile(filepath.Join(upstream, "tracked.txt"), []byte("new upstream\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, upstream, "add", "tracked.txt")
	gitTest(t, upstream, "commit", "-m", "advance")
	updated := gitTest(t, upstream, "rev-parse", "HEAD")
	m := manager(t)
	request, _ := requestFor(clone)
	request.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "origin", Name: "main"}
	request.Repositories[0].AutoFetch = true
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.Repositories[0].StartingCommit != updated {
		t.Fatal("stale remote branch used")
	}
	gitTest(t, clone, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "missing"))
	request.SessionID = domain.NewID()
	if _, err := m.Prepare(context.Background(), request); err == nil {
		t.Fatal("fetch failure used stale fallback")
	}
	request.SessionID = domain.NewID()
	request.Repositories[0].AutoFetch = false
	manifest, err = m.Prepare(context.Background(), request)
	if err != nil || manifest.Repositories[0].StartingCommit != updated {
		t.Fatalf("explicit disabled fetch did not use stored ref: %+v %v", manifest, err)
	}
}
func TestGeneralChatIsolationAndCancellation(t *testing.T) {
	m := manager(t)
	request := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	a, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	request.SessionID = domain.NewID()
	b, err := m.Prepare(context.Background(), request)
	if err != nil || a.PrimaryPath == b.PrimaryPath {
		t.Fatal("projectless sessions share a directory", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request.SessionID = domain.NewID()
	if _, err := m.Prepare(ctx, request); err == nil {
		t.Fatal("canceled preparation accepted")
	}
}
func TestDefaultBranchNeverGuessed(t *testing.T) {
	for _, inspection := range []Inspection{{Remotes: []string{}}, {Remotes: []string{"a", "b"}}, {Remotes: []string{"origin"}}} {
		if _, err := DefaultStarting(inspection, ""); err == nil {
			t.Fatal("guessed a missing default branch")
		}
	}
	ref, err := DefaultStarting(Inspection{Remotes: []string{"upstream"}, DefaultRefs: map[string]string{"upstream": "trunk"}}, "")
	if err != nil || ref.Name != "trunk" || ref.Remote != "upstream" {
		t.Fatal(ref, err)
	}
}
