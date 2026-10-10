package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func diffRequest(input PrepareRequest, manifest Manifest, comparison domain.WorkspaceDiffComparison, path string) ReadRequest {
	r := workspaceReadFixture(input, manifest, domain.WorkspaceGitDiff, path)
	r.Query.Comparison = comparison
	return r
}

func TestWorkspaceDiffKeepsOriginalCreationCommitAndLiveExecution(t *testing.T) {
	m := manager(t)
	source, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(source)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Close()
	before, _ := m.readExecutionClaim(input.SessionID)
	root := manifest.PrimaryPath
	write := func(name, value string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("tracked.txt", "committed change\n")
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "session commit")
	head := gitTest(t, root, "rev-parse", "HEAD")
	write("tracked.txt", "staged change\n")
	gitTest(t, root, "add", "tracked.txt")
	write("tracked.txt", "working change\n")
	write("untracked.txt", "untracked original\n")
	index := gitTest(t, root, "ls-files", "--stage")
	var previous string
	for _, mode := range []domain.WorkspaceDiffComparison{domain.DiffWorkingTree, domain.DiffStaged, domain.DiffCreation} {
		r, err := m.ReadWorkspace(context.Background(), diffRequest(input, manifest, mode, "."))
		if err != nil || r.Diff == nil {
			t.Fatal(mode, err)
		}
		v := r.Diff
		base, removed, added := head, "committed change", "working change"
		if mode == domain.DiffCreation {
			base, removed = manifest.Repositories[0].StartingCommit, "base"
		}
		if mode == domain.DiffStaged {
			added = "staged change"
		}
		if v.BaseObject != base || v.HeadCommit != head || !strings.Contains(v.Patch, "-"+removed) || !strings.Contains(v.Patch, "+"+added) || len(v.Untracked) != 1 || v.Untracked[0] != "untracked.txt" || strings.Contains(v.Patch, "untracked original") || v.Revision == previous {
			t.Fatal("comparison lost original Git scope", mode, v)
		}
		previous = v.Revision
	}
	if gitTest(t, root, "ls-files", "--stage") != index {
		t.Fatal("read changed index")
	}
	after, _ := m.readExecutionClaim(input.SessionID)
	if before != after {
		t.Fatal("diff changed live execution claim")
	}
	remaining, err := os.ReadDir(filepath.Join(m.Root, "workspace-read-processes"))
	if err != nil || len(remaining) != 0 {
		t.Fatal("diff retained owned children", err)
	}
}

func TestWorkspaceDiffLiteralPathBinaryAndNoExternalHelpers(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	root := manifest.PrimaryPath
	marker := filepath.Join(t.TempDir(), "helper-ran")
	// This command must never run, regardless of repository-defined drivers.
	gitTest(t, root, "config", "diff.external", "touch '"+marker+"'")
	gitTest(t, root, "config", "diff.fixture.textconv", "touch '"+marker+"'")
	gitTest(t, root, "config", "core.fsmonitor", "touch '"+marker+"'")
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("tracked.txt diff=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte{0, 1, 2}, 0600); err != nil {
		t.Fatal(err)
	}
	r, err := m.ReadWorkspace(context.Background(), diffRequest(input, manifest, domain.DiffWorkingTree, "tracked.txt"))
	if err != nil || !strings.Contains(r.Diff.Patch, "Binary files") || len(r.Diff.Untracked) != 0 {
		t.Fatal("binary/literal path semantics lost", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("external helper ran", err)
	}
	gitTest(t, root, "config", "filter.fixture.clean", "touch '"+marker+"'")
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("tracked.txt filter=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ReadWorkspace(context.Background(), diffRequest(input, manifest, domain.DiffWorkingTree, ".")); err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("configured filter silently applied or omitted", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("clean filter ran", err)
	}
	if _, err := m.ReadWorkspace(context.Background(), diffRequest(input, manifest, domain.DiffCreation, ".")); err == nil || domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("Local gained a creation baseline", err)
	}
}

func TestWorkspaceDiffIsolationCannotRunAnActiveCleanFilter(t *testing.T) {
	root := repository(t)
	marker := filepath.Join(t.TempDir(), "filter-ran")
	gitTest(t, root, "config", "filter.fixture.clean", "touch '"+marker+"'")
	if err := os.WriteFile(filepath.Join(root, ".gitattributes"), []byte("tracked.txt filter=fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("changed\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, root, "check-attr", "filter", "--", "tracked.txt"); !strings.Contains(got, "fixture") {
		t.Fatal("test did not activate the clean filter", got)
	}
	base := gitTest(t, root, "rev-parse", "HEAD")
	g := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), readOnly: true}
	patch, err := g.filterFreeDiff(context.Background(), root, []string{"diff", "--no-ext-diff", "--no-textconv", base, "--", "tracked.txt"})
	if err != nil || !strings.Contains(string(patch), "+changed") {
		t.Fatal("isolated diff lost the raw worktree change", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("isolated Git diff executed an active clean filter", err)
	}
}

func TestWorkspaceDiffUnbornAndBoundedResults(t *testing.T) {
	m, input, manifest := unbornLocalFixture(t)
	root := manifest.PrimaryPath
	readDiff := func(mode domain.WorkspaceDiffComparison) (domain.WorkspaceReadResult, error) {
		request := diffRequest(input, manifest, mode, "new.txt")
		// This test launches several real Git subprocesses. Use the full supported
		// read window so Windows full-suite contention does not consume its margin.
		// Revert to the shared deadline if this test stops exercising native Git.
		request.Deadline = time.Now().Add(16 * time.Second)
		return m.ReadWorkspace(context.Background(), request)
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte("initial\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "new.txt")
	for _, mode := range []domain.WorkspaceDiffComparison{domain.DiffWorkingTree, domain.DiffStaged} {
		r, err := readDiff(mode)
		if err != nil || r.Diff.Base != domain.DiffEmptyTree || r.Diff.HeadCommit != "" || !strings.Contains(r.Diff.Patch, "+initial") {
			t.Fatal("unborn diff invented a commit", mode, err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "new.txt"), []byte(strings.Repeat("larger\n", 16000)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readDiff(domain.DiffWorkingTree); err == nil || domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("oversized diff silently truncated", err)
	}
}

func TestWorkspaceBranchDiffUsesMergeBaseAndLocalOptions(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	root := manifest.PrimaryPath
	base := gitTest(t, root, "rev-parse", "HEAD")
	gitTest(t, root, "branch", "review-base", base)
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("committed branch change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	gitTest(t, root, "commit", "-m", "branch change")
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("live branch change\n"), 0600); err != nil {
		t.Fatal(err)
	}
	q := diffRequest(input, manifest, domain.DiffBranch, ".")
	q.Query.BaseRef = domain.Reference{Type: domain.LocalBranch, Name: "review-base"}
	result, err := m.ReadWorkspace(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	diff := result.Diff
	if diff.BaseCommit != base || diff.MergeBase != base || diff.BaseObject != base || diff.HeadCommit == base || !strings.Contains(diff.Patch, "-base") || !strings.Contains(diff.Patch, "+live branch change") {
		t.Fatal("branch observation lost merge-base scope", diff)
	}
	optionsRequest := workspaceReadFixture(input, manifest, domain.WorkspaceGitDiffOptions, ".")
	options, err := m.ReadWorkspace(context.Background(), optionsRequest)
	if err != nil || options.DiffOptions == nil {
		t.Fatal("local options unavailable", err)
	}
	if options.DiffOptions.Default != (domain.Reference{}) {
		t.Fatal("manifest Base/Starting or Local HEAD became an explicit default", options.DiffOptions)
	}
	found := false
	for _, choice := range options.DiffOptions.Choices {
		if choice.Reference == q.Query.BaseRef && choice.Available {
			found = true
		}
	}
	if !found {
		t.Fatal("complete local branch inventory omitted base", options.DiffOptions)
	}
	q.Query.BaseRef.Name = "missing"
	if _, err := m.ReadWorkspace(context.Background(), q); err == nil || domain.SafeError(err).Code != domain.Unavailable {
		t.Fatal("missing local base substituted", err)
	}
}

func TestWorkspaceDiffOptionsKeepsSavedMissingBaseWithoutFallback(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "branch", "configured-base")
	input, _ := requestFor(root)
	input.Type = domain.Local
	input.Repositories[0].Base = domain.Reference{Type: domain.LocalBranch, Name: "configured-base"}
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	q := workspaceReadFixture(input, manifest, domain.WorkspaceGitDiffOptions, ".")
	r, err := m.ReadWorkspace(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.DiffOptions.Default != input.Repositories[0].Base {
		t.Fatal("saved base lost", r.DiffOptions)
	}
	gitTest(t, root, "branch", "-D", "configured-base")
	r, err = m.ReadWorkspace(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	if r.DiffOptions.Default != (domain.Reference{}) {
		t.Fatal("missing saved base substituted", r.DiffOptions)
	}
	found := false
	for _, choice := range r.DiffOptions.Choices {
		if choice.Reference == input.Repositories[0].Base {
			found = choice.Configured && !choice.Available
		}
	}
	if !found {
		t.Fatal("missing configured reference disappeared", r.DiffOptions)
	}
}
