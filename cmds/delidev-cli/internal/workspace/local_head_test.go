package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func unbornLocalFixture(t *testing.T) (*Manager, PrepareRequest, Manifest) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "init", "-b", "first-branch")
	if err := os.WriteFile(filepath.Join(root, "staged.txt"), []byte("staged content"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "staged.txt")
	if err := os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("untracked content"), 0600); err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(root)
	input.Type = domain.Local
	// Local must ignore this nonexistent selection, even for an unborn checkout.
	input.Repositories[0].AutoFetch = true
	input.Repositories[0].Starting = domain.Reference{Type: domain.RemoteBranch, Remote: "absent", Name: "absent"}
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return m, input, manifest
}

func TestLocalUnbornExecutionPreservesIndexAndTransitionsToFirstCommit(t *testing.T) {
	m, input, manifest := unbornLocalFixture(t)
	root := manifest.PrimaryPath
	repo := manifest.Repositories[0]
	if repo.LocalHEAD != LocalHEADUnborn || repo.StartingCommit != "" || repo.BaseCommit != "" || repo.Starting != (domain.Reference{}) || repo.Owned {
		t.Fatal("unborn preparation fabricated a commit", repo)
	}
	if err := ValidateResult(input, manifest, runtime.GOOS); err != nil {
		t.Fatal(err)
	}
	index, err := os.ReadFile(filepath.Join(root, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	first := closeFirstExecution(t, m, input, manifest)
	inspection, err := m.InspectClosedExecution(context.Background(), first, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	if current, err := os.ReadFile(filepath.Join(root, ".git", "index")); err != nil || string(current) != string(index) {
		t.Fatal("execution changed the original index", err)
	}
	if gitTest(t, root, "symbolic-ref", "HEAD") != "refs/heads/first-branch" {
		t.Fatal("execution selected another branch")
	}
	if gitTest(t, root, "status", "--porcelain") != "A  staged.txt\n?? untracked.txt" {
		t.Fatal("execution changed staged/untracked content")
	}
	// Commit only the user's staged file after the original empty-branch turn.
	gitTest(t, root, "commit", "-m", "first user commit")
	head := gitTest(t, root, "rev-parse", "HEAD")
	next, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), first, input, manifest)
	if err != nil {
		t.Fatal("first commit invalidated Local continuation", err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	retained, err := m.Read(input.SessionID)
	if err != nil || retained.Repositories[0].LocalHEAD != LocalHEADUnborn || retained.Repositories[0].StartingCommit != "" {
		t.Fatal("continuation rewrote historical preparation", err)
	}
	if gitTest(t, root, "rev-parse", "HEAD") != head {
		t.Fatal("continuation changed user commit")
	}
	if raw, err := os.ReadFile(filepath.Join(root, "untracked.txt")); err != nil || string(raw) != "untracked content" {
		t.Fatal("continuation changed untracked content", err)
	}
}

func TestLocalHEADRejectsBrokenReferencesAndExecutionFailures(t *testing.T) {
	for _, scenario := range []string{"malformed-ref", "missing-object", "zero-object", "detached-missing", "non-branch", "missing-executable", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			m, input, manifest := unbornLocalFixture(t)
			root := manifest.PrimaryPath
			path, content := filepath.Join(root, ".git", "refs", "heads", "first-branch"), "malformed\n"
			switch scenario {
			case "missing-object":
				content = strings.Repeat("a", 40) + "\n"
			case "zero-object":
				content = strings.Repeat("0", 40) + "\n"
			case "detached-missing":
				path, content = filepath.Join(root, ".git", "HEAD"), strings.Repeat("a", 40)+"\n"
			case "non-branch":
				path, content = filepath.Join(root, ".git", "HEAD"), "ref: refs/tags/absent\n"
			}
			ctx := context.Background()
			switch scenario {
			case "missing-executable":
				m.Git.Executable = filepath.Join(t.TempDir(), "absent-git")
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			default:
				if err := os.WriteFile(path, []byte(content), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := m.ClaimFirstExecution(ctx, domain.NewID(), domain.NewID(), input, manifest); err == nil {
				t.Fatal("broken HEAD became an unborn execution")
			}
			if _, err := os.Stat(filepath.Join(root, "staged.txt")); err != nil {
				t.Fatal("refusal removed user content", err)
			}
		})
	}
}

func TestLocalResultRequiresExplicitUnbornStateAndPreservesCommittedEncoding(t *testing.T) {
	_, input, manifest := unbornLocalFixture(t)
	for _, scenario := range []string{"missing-state", "unknown-state", "fabricated-commit", "starting-ref", "owned", "worktree"} {
		t.Run(scenario, func(t *testing.T) {
			raw, _ := json.Marshal(manifest)
			var changed Manifest
			if err := json.Unmarshal(raw, &changed); err != nil {
				t.Fatal(err)
			}
			request := input
			repo := &changed.Repositories[0]
			switch scenario {
			case "missing-state":
				repo.LocalHEAD = LocalHEADCommitted
			case "unknown-state":
				repo.LocalHEAD = "unknown"
			case "fabricated-commit":
				repo.StartingCommit, repo.BaseCommit = strings.Repeat("a", 40), strings.Repeat("a", 40)
			case "starting-ref":
				repo.Starting = domain.Reference{Type: domain.LocalBranch, Name: "first-branch"}
			case "owned":
				repo.Owned = true
			case "worktree":
				request.Type, changed.Type = domain.Worktree, domain.Worktree
				changed.InputDigest = preparationDigest(request)
			}
			if err := ValidateResult(request, changed, runtime.GOOS); err == nil {
				t.Fatal("invalid unborn manifest accepted")
			}
		})
	}
	_, _, committed := localExecutionFixture(t)
	raw, _ := json.Marshal(committed)
	if strings.Contains(string(raw), "local_head") {
		t.Fatal("committed Local encoding changed existing checkpoint bytes")
	}
}
