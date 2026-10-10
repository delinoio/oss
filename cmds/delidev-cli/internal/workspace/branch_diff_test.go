package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestBranchDiffUsesAcceptedBaseAndIncludesCommittedStagedWorkingChanges(t *testing.T) {
	m := manager(t)
	source, _ := filepath.EvalSymlinks(repository(t))
	gitTest(t, source, "checkout", "-b", "starting")
	os.WriteFile(filepath.Join(source, "tracked.txt"), []byte("starting commit\n"), 0600)
	gitTest(t, source, "add", "tracked.txt")
	gitTest(t, source, "commit", "-m", "different Starting")
	input, _ := requestFor(source)
	input.Repositories[0].Starting = domain.Reference{Type: domain.LocalBranch, Name: "starting"}
	input.Repositories[0].Base = domain.Reference{Type: domain.LocalBranch, Name: "main"}
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	root := manifest.PrimaryPath
	base := manifest.Repositories[0].BaseCommit
	for _, name := range []string{"committed.txt", "staged.txt", "working.txt"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("base\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	gitTest(t, root, "add", ".")
	gitTest(t, root, "commit", "-m", "feature files")
	os.WriteFile(filepath.Join(root, "committed.txt"), []byte("committed\n"), 0600)
	gitTest(t, root, "add", "committed.txt")
	gitTest(t, root, "commit", "-m", "committed feature")
	os.WriteFile(filepath.Join(root, "staged.txt"), []byte("staged\n"), 0600)
	gitTest(t, root, "add", "staged.txt")
	os.WriteFile(filepath.Join(root, "working.txt"), []byte("working\n"), 0600)
	os.WriteFile(filepath.Join(root, "untracked.txt"), []byte("private untracked\n"), 0600)
	before := gitTest(t, root, "ls-files", "--stage")
	optionsRequest := workspaceReadFixture(input, manifest, domain.WorkspaceGitDiffOptions, ".")
	options, err := m.ReadWorkspace(context.Background(), optionsRequest)
	if err != nil || options.DiffOptions == nil || !options.DiffOptions.DefaultAvailable || *options.DiffOptions.Default != input.Repositories[0].Base {
		t.Fatal("accepted base lost", err)
	}
	request := diffRequest(input, manifest, domain.DiffBranch, ".")
	request.Query.BaseRef = options.DiffOptions.Default
	result, err := m.ReadWorkspace(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.Diff.BaseCommit != base || result.Diff.MergeBase != base || !strings.Contains(result.Diff.Patch, "+committed") || !strings.Contains(result.Diff.Patch, "+staged") || !strings.Contains(result.Diff.Patch, "+working") || strings.Contains(result.Diff.Patch, "private untracked") || len(result.Diff.Untracked) != 1 {
		t.Fatal("branch scope lost")
	}
	if gitTest(t, root, "ls-files", "--stage") != before {
		t.Fatal("observation changed index")
	}
	gitTest(t, root, "update-ref", "-d", "refs/heads/main")
	options, err = m.ReadWorkspace(context.Background(), optionsRequest)
	if err != nil || options.DiffOptions.DefaultAvailable || *options.DiffOptions.Default != input.Repositories[0].Base {
		t.Fatal("missing saved base fell back", err)
	}
	if _, err = m.ReadWorkspace(context.Background(), request); err == nil {
		t.Fatal("missing base silently substituted")
	}
}

func TestLocalDiffOptionsNeverInferExplicitBaseFromCapturedHEAD(t *testing.T) {
	m, input, manifest := localExecutionFixture(t)
	if input.Repositories[0].Base != (domain.Reference{}) {
		t.Fatal("fixture must omit Base")
	}
	r := workspaceReadFixture(input, manifest, domain.WorkspaceGitDiffOptions, ".")
	value, err := m.ReadWorkspace(context.Background(), r)
	if err != nil || value.DiffOptions.Default != nil || value.DiffOptions.DefaultAvailable {
		t.Fatal("captured Local HEAD became explicit base", err)
	}
}
func TestBranchDiffRejectsUnbornAndUnrelatedHistory(t *testing.T) {
	m, input, manifest := unbornLocalFixture(t)
	ref := domain.Reference{Type: domain.LocalBranch, Name: "missing"}
	r := diffRequest(input, manifest, domain.DiffBranch, ".")
	r.Query.BaseRef = &ref
	if _, err := m.ReadWorkspace(context.Background(), r); err == nil {
		t.Fatal("unborn branch comparison accepted")
	}
	root := repository(t)
	gitTest(t, root, "checkout", "--orphan", "unrelated")
	gitTest(t, root, "commit", "--allow-empty", "-m", "unrelated history")
	g := Git{ProcessRoot: filepath.Join(t.TempDir(), "processes"), OwnerID: domain.NewID(), readOnly: true}
	head := gitTest(t, root, "rev-parse", "HEAD")
	ref = domain.Reference{Type: domain.LocalBranch, Name: "main"}
	if _, _, err := g.branchBase(context.Background(), PreparedRepository{Path: root}, &ref, head); err == nil {
		t.Fatal("unrelated histories substituted")
	}
}

func TestBranchDefaultUsesOnlyLocalRemoteHEADPolicy(t *testing.T) {
	cases := []struct {
		inspection  Inspection
		preferred   string
		remote      string
		unavailable bool
	}{
		{Inspection{Remotes: []string{"origin", "upstream"}, DefaultRefs: map[string]string{"origin": "main", "upstream": "trunk"}}, "upstream", "upstream", false},
		{Inspection{Remotes: []string{"origin", "upstream"}, DefaultRefs: map[string]string{"origin": "main", "upstream": "trunk"}}, "", "origin", false},
		{Inspection{Remotes: []string{"upstream"}, DefaultRefs: map[string]string{"upstream": "trunk"}}, "", "upstream", false},
		{Inspection{Remotes: []string{"origin"}, DefaultRefs: map[string]string{}}, "", "", true},
		{Inspection{Remotes: []string{"a", "b"}, DefaultRefs: map[string]string{"a": "main", "b": "main"}}, "", "", true},
		{Inspection{Remotes: []string{"origin"}, DefaultRefs: map[string]string{"origin": "main"}}, "missing", "", true},
	}
	for _, c := range cases {
		ref, err := DefaultStarting(c.inspection, c.preferred)
		if (err != nil) != c.unavailable || err == nil && ref.Remote != c.remote {
			t.Fatal("default policy guessed/fell back", ref, err)
		}
	}
}
