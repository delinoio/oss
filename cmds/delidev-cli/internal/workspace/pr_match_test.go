package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func prMatchFixture(t *testing.T, kind domain.WorkspaceType) (prPreparationFixture, ReadRequest) {
	t.Helper()
	f := newPRPreparationFixture(t)
	target := *f.request.Repositories[0].PRTarget
	// Model an ordinary pre-existing project session, not a PR-prepared one.
	// Fixture setup imports its later commit from an isolated local repository.
	gitTest(t, f.checkout, "fetch", f.fork, f.head)
	spec := &f.request.Repositories[0]
	spec.PRTarget, spec.AutoFetch = nil, false
	spec.Starting = domain.Reference{Type: domain.CommitReference, Name: f.base}
	if kind == domain.Local {
		gitTest(t, f.checkout, "switch", "-c", "feature", f.head)
		f.request.Type, f.request.OriginMachineID = kind, f.request.MachineID
		spec.Starting = domain.Reference{}
	}
	manifest, err := f.manager.Prepare(context.Background(), f.request)
	if err != nil {
		t.Fatal(err)
	}
	return f, ReadRequest{ID: domain.NewID(), Deadline: time.Now().UTC().Add(15 * time.Second), Preparation: f.request, Manifest: manifest, PRCandidate: &target}
}

func TestPRWorkspaceMatchReadsCurrentHeadWithoutTakingExecutionOwnership(t *testing.T) {
	for _, kind := range []domain.WorkspaceType{domain.Worktree, domain.Local} {
		t.Run(string(kind), func(t *testing.T) {
			f, request := prMatchFixture(t, kind)
			lease, err := f.manager.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), request.Preparation, request.Manifest)
			if err != nil {
				t.Fatal(err)
			}
			defer lease.Close()
			if kind == domain.Worktree {
				gitTest(t, request.Manifest.PrimaryPath, "switch", "--detach", f.head)
			}
			claim := readPRStartupTestFile(t, f.manager.executionClaimPath(request.Preparation.SessionID))
			manifestPath := filepath.Join(f.manager.Root, "workspaces", string(request.Preparation.SessionID), "manifest.json")
			original := readPRStartupTestFile(t, manifestPath)
			root := request.Manifest.PrimaryPath
			indexPath := gitTest(t, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
			index := readPRStartupTestFile(t, indexPath)
			fetchPath := filepath.Join(f.checkout, ".git", "FETCH_HEAD")
			fetchHead, refs := readPRStartupTestFile(t, fetchPath), gitTest(t, root, "show-ref")
			// The server starts the bounded read after workspace/execution setup.
			// Native lease setup must not consume this fixture's observation budget.
			request.Deadline = time.Now().UTC().Add(15 * time.Second)
			result, err := f.manager.MatchPRWorkspace(context.Background(), request)
			if err != nil || result.State != PRWorkspaceMatches || ValidatePRWorkspaceMatch(request, result) != nil {
				t.Fatal("current native head did not match", err)
			}
			if original != readPRStartupTestFile(t, manifestPath) || claim != readPRStartupTestFile(t, f.manager.executionClaimPath(request.Preparation.SessionID)) || index != readPRStartupTestFile(t, indexPath) || fetchHead != readPRStartupTestFile(t, fetchPath) || refs != gitTest(t, root, "show-ref") {
				t.Fatal("read changed original manifest, active execution, index, FETCH_HEAD or refs")
			}
			if strings.Contains(readPRStartupTestFile(t, f.log), "network:yes") {
				t.Fatal("workspace matching fetched objects")
			}
			if _, err := os.Stat(filepath.Join(f.manager.readProcessRoot(request.Preparation.SessionID), string(request.ID))); !os.IsNotExist(err) {
				t.Fatal("read process ownership was not independently cleaned", err)
			}
			for _, change := range []func(*PRWorkspaceMatch){
				func(r *PRWorkspaceMatch) { r.ReadID = domain.NewID() },
				func(r *PRWorkspaceMatch) { r.RepositoryID = domain.NewID() },
				func(r *PRWorkspaceMatch) { r.SelectionDigest = strings.Repeat("0", 64) },
				func(r *PRWorkspaceMatch) { r.State = "unknown" },
				func(r *PRWorkspaceMatch) { r.ObservedAt = request.Deadline.Add(-time.Minute) },
			} {
				changed := result
				change(&changed)
				if ValidatePRWorkspaceMatch(request, changed) == nil {
					t.Fatal("foreign matching proof accepted")
				}
			}
			changed := request
			target := *request.PRCandidate
			target.HeadSHA = f.next
			changed.PRCandidate = &target
			if ValidatePRWorkspaceMatch(changed, result) == nil {
				t.Fatal("match borrowed for another PR head")
			}
			raw, _ := json.Marshal(result)
			var ordinary domain.WorkspaceReadResult
			if domain.Decode(raw, &ordinary) == nil {
				t.Fatal("private match accepted as public file content")
			}
		})
	}
}

func TestPRWorkspaceMatchPreservesMismatchesAndDistinguishesUnknownAccess(t *testing.T) {
	f, request := prMatchFixture(t, domain.Worktree)
	root := request.Manifest.PrimaryPath
	gitTest(t, root, "switch", "--detach", f.head)
	read := func(want PRWorkspaceMatchState, code domain.Code) {
		t.Helper()
		request.ID, request.Deadline = domain.NewID(), time.Now().UTC().Add(15*time.Second)
		result, err := f.manager.MatchPRWorkspace(context.Background(), request)
		if code != "" {
			if err == nil || domain.SafeError(err).Code != code || result.Version != 0 {
				t.Fatal("unknown access became matching evidence", err)
			}
		} else if err != nil || result.State != want {
			t.Fatal("wrong native workspace match", result.State, err)
		}
	}
	gitTest(t, root, "switch", "-c", "unrelated")
	read(PRWorkspaceDifferent, "")
	gitTest(t, root, "switch", "--detach", f.head)
	tracked := filepath.Join(root, "tracked.txt")
	if err := os.WriteFile(tracked, []byte("unrelated staged changes\n"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, root, "add", "tracked.txt")
	if err := os.WriteFile(tracked, []byte("PR head\n"), 0600); err != nil {
		t.Fatal(err)
	}
	indexPath := gitTest(t, root, "rev-parse", "--path-format=absolute", "--git-path", "index")
	index := readPRStartupTestFile(t, indexPath)
	read(PRWorkspaceDifferent, "")
	if index != readPRStartupTestFile(t, indexPath) {
		t.Fatal("matching repaired an unrelated index")
	}
	gitTest(t, root, "reset", "--", "tracked.txt")
	if err := os.WriteFile(filepath.Join(f.checkout, ".git", "info", "exclude"), []byte("unrelated\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ignored := filepath.Join(root, "unrelated")
	if err := os.WriteFile(ignored, []byte("retain ignored data"), 0600); err != nil {
		t.Fatal(err)
	}
	read(PRWorkspaceDifferent, "")
	if readPRStartupTestFile(t, ignored) != "retain ignored data" {
		t.Fatal("ignored data was changed")
	}
	if err := os.Remove(ignored); err != nil {
		t.Fatal(err)
	}
	gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.next)
	read(PRWorkspaceDifferent, "")
	gitTest(t, f.fork, "update-ref", "refs/heads/feature", f.head)
	if err := os.WriteFile(f.marker, []byte("fail"), 0600); err != nil {
		t.Fatal(err)
	}
	read("", domain.Unavailable)
	if err := os.Remove(f.marker); err != nil {
		t.Fatal(err)
	}
	read(PRWorkspaceMatches, "")
	before := readPRStartupTestFile(t, f.log)
	request.Query = domain.WorkspaceReadQuery{Operation: domain.WorkspaceDirectory, Path: "."}
	read("", domain.Unsupported)
	if _, err := f.manager.ReadWorkspace(context.Background(), request); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("mixed private/public profile accepted", err)
	}
	if before != readPRStartupTestFile(t, f.log) {
		t.Fatal("mixed profile performed Git networking")
	}
}
