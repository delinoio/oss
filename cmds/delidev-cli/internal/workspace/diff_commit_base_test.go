package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// commitBaseFixture prepares a Local repository whose first commit is not
// named by any branch, tag or configured Base, followed by one committed
// change. Only refs/heads/main appears in the options inventory.
func commitBaseFixture(t *testing.T) (*Manager, PrepareRequest, Manifest, string, string) {
	t.Helper()
	m, input, manifest := localExecutionFixture(t)
	root := manifest.PrimaryPath
	base := gitTest(t, root, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "change")
	return m, input, manifest, root, base
}

func commitBaseRequest(input PrepareRequest, manifest Manifest, name string) ReadRequest {
	q := diffRequest(input, manifest, domain.DiffBranch, ".")
	q.Query.BaseRef = domain.Reference{Type: domain.CommitReference, Name: name}
	return q
}

func TestWorkspaceBranchDiffResolvesUnconfiguredFullCommitWithoutInventory(t *testing.T) {
	m, input, manifest, root, base := commitBaseFixture(t)
	options, err := m.ReadWorkspace(context.Background(), workspaceReadFixture(input, manifest, domain.WorkspaceGitDiffOptions, "."))
	if err != nil || options.DiffOptions == nil {
		t.Fatal("local options unavailable", err)
	}
	for _, choice := range options.DiffOptions.Choices {
		if choice.Reference.Type == domain.CommitReference {
			t.Fatal("options inventory enumerated a commit object", choice)
		}
	}
	refsBefore := gitTest(t, root, "for-each-ref")
	indexBefore := gitTest(t, root, "status", "--porcelain=v1")
	result, err := m.ReadWorkspace(context.Background(), commitBaseRequest(input, manifest, base))
	if err != nil {
		t.Fatal("explicit local commit rejected", err)
	}
	diff := result.Diff
	if diff == nil || diff.BaseRef != (domain.Reference{Type: domain.CommitReference, Name: base}) || diff.BaseCommit != base || diff.MergeBase != base || diff.BaseObject != base || diff.HeadCommit == base {
		t.Fatal("commit base identity changed", diff)
	}
	if !strings.Contains(diff.Patch, "-base") || !strings.Contains(diff.Patch, "+changed") {
		t.Fatal("commit base patch missing tracked change", diff.Patch)
	}
	if gitTest(t, root, "for-each-ref") != refsBefore || gitTest(t, root, "status", "--porcelain=v1") != indexBefore {
		t.Fatal("comparison mutated refs or working files")
	}
}

func TestWorkspaceBranchDiffRejectsInvalidCommitBasesWithoutFallback(t *testing.T) {
	m, input, manifest, root, base := commitBaseFixture(t)
	blob := gitTest(t, root, "rev-parse", "HEAD:tracked.txt")
	tree := gitTest(t, root, "rev-parse", "HEAD^{tree}")
	absent := strings.Repeat("a", len(base))
	for name, tc := range map[string]string{
		"abbreviated": base[:12], "uppercase": strings.ToUpper(base), "absent": absent, "blob": blob, "tree": tree,
		"non-hex": strings.Repeat("g", len(base)), "branch-name": "main",
	} {
		t.Run(name, func(t *testing.T) {
			result, err := m.ReadWorkspace(context.Background(), commitBaseRequest(input, manifest, tc))
			if err == nil || result.Diff != nil {
				t.Fatal("invalid commit base produced an observation", result.Diff)
			}
			switch code := domain.SafeError(err).Code; code {
			case domain.InvalidArgument, domain.Unavailable:
			default:
				t.Fatal("unexpected failure class", code, err)
			}
		})
	}
}

func TestWorkspaceBranchDiffRejectsUnrelatedAndAmbiguousCommitHistory(t *testing.T) {
	m, input, manifest, root, _ := commitBaseFixture(t)
	gitTest(t, root, "switch", "--orphan", "unrelated")
	if err := os.WriteFile(filepath.Join(root, "other.txt"), []byte("other\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "other.txt")
	gitTest(t, root, "commit", "-m", "unrelated root")
	unrelated := gitTest(t, root, "rev-parse", "HEAD")
	gitTest(t, root, "switch", "main")
	if _, err := m.ReadWorkspace(context.Background(), commitBaseRequest(input, manifest, unrelated)); err == nil || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("unrelated history compared", err)
	}

	// A criss-cross merge leaves two equally good merge-bases.
	gitTest(t, root, "switch", "-c", "left", "main")
	gitTest(t, root, "commit", "--allow-empty", "-m", "left")
	left := gitTest(t, root, "rev-parse", "HEAD")
	gitTest(t, root, "switch", "-c", "right", "main")
	gitTest(t, root, "commit", "--allow-empty", "-m", "right")
	right := gitTest(t, root, "rev-parse", "HEAD")
	gitTest(t, root, "switch", "left")
	gitTest(t, root, "merge", "--no-ff", "-m", "merge right into left", right)
	leftMerge := gitTest(t, root, "rev-parse", "HEAD")
	gitTest(t, root, "switch", "right")
	gitTest(t, root, "merge", "--no-ff", "-m", "merge left into right", left)
	gitTest(t, root, "switch", "main")
	gitTest(t, root, "reset", "--hard", "right")
	if got := strings.Fields(gitTest(t, root, "merge-base", "--all", "HEAD", leftMerge)); len(got) != 2 {
		t.Skip("git did not produce an ambiguous merge-base fixture", got)
	}
	if _, err := m.ReadWorkspace(context.Background(), commitBaseRequest(input, manifest, leftMerge)); err == nil || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("ambiguous merge-base compared", err)
	}
}

func TestWorkspaceBranchDiffResolvesSHA256CommitBaseWhenGitSupportsIt(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	probe := exec.Command("git", "-C", root, "init", "-b", "main", "--object-format=sha256")
	if out, err := probe.CombinedOutput(); err != nil {
		t.Skipf("git cannot create SHA-256 repositories: %v %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("base\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "initial")
	input, _ := requestFor(root)
	input.Type = domain.Local
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Skipf("worker preparation does not support SHA-256 repositories: %v", err)
	}
	base := gitTest(t, root, "rev-parse", "HEAD")
	if len(base) != 64 {
		t.Fatal("fixture is not a SHA-256 repository", base)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "change")
	result, err := m.ReadWorkspace(context.Background(), commitBaseRequest(input, manifest, base))
	if err != nil {
		t.Fatal("explicit 64-digit commit rejected", err)
	}
	diff := result.Diff
	if diff == nil || diff.BaseCommit != base || diff.MergeBase != base || diff.BaseRef.Name != base || !strings.Contains(diff.Patch, "+changed") {
		t.Fatal("64-digit commit base identity changed", diff)
	}
}
