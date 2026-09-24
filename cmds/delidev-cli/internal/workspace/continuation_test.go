package workspace

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func closeFirstExecution(t *testing.T, m *Manager, input PrepareRequest, manifest Manifest) ExecutionPredecessor {
	t.Helper()
	previous := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
	lease, err := m.ClaimFirstExecution(context.Background(), previous.JobID, previous.ExecutionID, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	return previous
}

func TestContinuationPreservesCommittedAndDirtyWorktree(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(root)
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	previous := closeFirstExecution(t, m, input, manifest)
	before, err := security.ReadPrivate(m.executionClaimPath(input.SessionID), 4096)
	if err != nil {
		t.Fatal(err)
	}
	// Actual agent commits/branch changes do not rewrite the original creation
	// manifest and must not be reset during a legitimate native continuation.
	gitTest(t, manifest.PrimaryPath, "checkout", "-b", "agent-changes")
	if err = os.WriteFile(filepath.Join(manifest.PrimaryPath, "agent.txt"), []byte("agent commit"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, manifest.PrimaryPath, "add", "agent.txt")
	gitTest(t, manifest.PrimaryPath, "commit", "-m", "agent changes")
	head := gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD")
	if head == manifest.Repositories[0].StartingCommit {
		t.Fatal("fixture did not advance HEAD")
	}
	dirty := filepath.Join(manifest.PrimaryPath, "untracked.txt")
	if err = os.WriteFile(dirty, []byte("keep dirty content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = m.verifyRecoveredReady(context.Background(), input, manifest); err == nil {
		t.Fatal("preparation recovery adopted a changed first-execution HEAD")
	}
	next := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
	lease, err := m.ClaimContinuation(context.Background(), next.JobID, next.ExecutionID, previous, input, manifest)
	if err != nil {
		t.Fatal("valid owned continuation rejected", err)
	}
	t.Cleanup(func() { lease.Close() })
	if _, err = m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest); err == nil {
		t.Fatal("concurrent continuation crossed the session lock")
	}
	history, err := security.ReadPrivate(m.executionHistoryPath(input.SessionID, previous.ExecutionID), 4096)
	if err != nil || !bytes.Equal(before, history) {
		t.Fatal("advancing ownership rewrote prior cleanup evidence")
	}
	if bytes.Contains(history, []byte(manifest.PrimaryPath)) || bytes.Contains(history, []byte("agent-changes")) {
		t.Fatal("ownership history leaked paths or repository content")
	}
	active, err := m.readExecutionClaim(input.SessionID)
	if err != nil || active.Version != 2 || active.State != executionClaimActive || active.PreviousJobID != previous.JobID || active.PreviousExecutionID != previous.ExecutionID || active.JobID != next.JobID {
		t.Fatal("missing exact predecessor binding")
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD"); got != head {
		t.Fatal("continuation changed HEAD")
	}
	if got := gitTest(t, manifest.PrimaryPath, "branch", "--show-current"); got != "agent-changes" {
		t.Fatal("continuation changed the selected branch")
	}
	if got, err := os.ReadFile(dirty); err != nil || string(got) != "keep dirty content" {
		t.Fatal("continuation changed uncommitted files")
	}
	if got := gitTest(t, root, "branch", "--show-current"); got != "main" {
		t.Fatal("continuation changed original checkout")
	}
	retained, err := m.Read(input.SessionID)
	if err != nil || retained.Repositories[0].StartingCommit != manifest.Repositories[0].StartingCommit {
		t.Fatal("continuation rewrote preparation provenance")
	}
	replacement := &Manager{Root: m.Root, Logger: m.Logger}
	third, err := replacement.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), next, input, manifest)
	if err != nil {
		t.Fatal("closed successor could not continue across Worker reconstruction", err)
	}
	if err = third.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err = replacement.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest); err == nil {
		t.Fatal("old predecessor was reused after ownership advanced")
	}
	if _, err = replacement.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest); err == nil {
		t.Fatal("continuation enabled a first-input replay")
	}
}

func TestContinuationRejectsUnprovenPredecessorWithoutReplacement(t *testing.T) {
	for _, variant := range []string{"active", "missing-claim", "missing-process-index", "foreign-job", "foreign-execution", "legacy", "invalid-digest", "invalid-version", "history-conflict", "existing-process-scope", "changed-manifest", "canceled"} {
		t.Run(variant, func(t *testing.T) {
			m, input, manifest := chatExecutionFixture(t)
			previous := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
			lease, err := m.ClaimFirstExecution(context.Background(), previous.JobID, previous.ExecutionID, input, manifest)
			if err != nil {
				t.Fatal(err)
			}
			if variant == "active" {
				if err = lease.release(); err != nil {
					t.Fatal(err)
				}
			} else if err = lease.Close(); err != nil {
				t.Fatal(err)
			}
			job, execution := domain.NewID(), domain.NewID()
			ctx := context.Background()
			switch variant {
			case "missing-claim":
				err = os.Remove(m.executionClaimPath(input.SessionID))
			case "missing-process-index":
				err = os.Remove(filepath.Join(m.Git.ProcessRoot, string(previous.JobID)))
			case "foreign-job":
				previous.JobID = domain.NewID()
			case "foreign-execution":
				previous.ExecutionID = domain.NewID()
			case "legacy", "invalid-digest", "invalid-version":
				claim, e := m.readExecutionClaim(input.SessionID)
				if e != nil {
					t.Fatal(e)
				}
				if variant == "legacy" {
					claim.Version = 1
					claim.WorkspaceDigest = ""
				} else if variant == "invalid-digest" {
					claim.WorkspaceDigest = "invalid"
				} else {
					claim.Version = 3
				}
				raw, _ := json.Marshal(claim)
				err = security.WriteAtomic(m.executionClaimPath(input.SessionID), raw)
			case "history-conflict":
				path := m.executionHistoryPath(input.SessionID, previous.ExecutionID)
				if e := security.PrivateDir(filepath.Dir(path)); e != nil {
					t.Fatal(e)
				}
				err = security.WriteAtomic(path, []byte(`{"forged":true}`))
			case "existing-process-scope":
				err = security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(job)))
			case "changed-manifest":
				manifest.CreatedAt = manifest.CreatedAt.Add(1)
				raw, _ := json.Marshal(manifest)
				err = security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(input.SessionID), "manifest.json"), raw)
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			if err != nil {
				t.Fatal(err)
			}
			before, readErr := os.ReadFile(m.executionClaimPath(input.SessionID))
			_, err = m.ClaimContinuation(ctx, job, execution, previous, input, manifest)
			if err == nil {
				t.Fatal("unproven predecessor acquired continuation")
			}
			after, afterErr := os.ReadFile(m.executionClaimPath(input.SessionID))
			if (readErr == nil) != (afterErr == nil) || !bytes.Equal(before, after) {
				t.Fatal("failed continuation replaced original ownership")
			}
			if variant != "existing-process-scope" {
				if _, e := os.Lstat(filepath.Join(m.Git.ProcessRoot, string(job))); !os.IsNotExist(e) {
					t.Fatal("failed preflight created successor process ownership")
				}
			}
		})
	}
}

func TestContinuationRejectsChangedGitAdministrativeIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	input, _ := requestFor(root)
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	previous := closeFirstExecution(t, m, input, manifest)
	otherInput := input
	otherInput.SessionID = domain.NewID()
	other, err := m.Prepare(context.Background(), otherInput)
	if err != nil {
		t.Fatal(err)
	}
	foreign, err := os.ReadFile(filepath.Join(other.PrimaryPath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(foreign, original) {
		t.Fatal("fixture did not create distinct worktree ownership")
	}
	if err = os.WriteFile(filepath.Join(manifest.PrimaryPath, ".git"), foreign, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest); err == nil {
		t.Fatal("another registered worktree was adopted under the old path")
	}
	if err = os.WriteFile(filepath.Join(manifest.PrimaryPath, ".git"), original, 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
	if err != nil {
		t.Fatal("failed check consumed predecessor", err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestContinuationHistoryPreventsOldExecutionReuseAndRetainsFiles(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	first := closeFirstExecution(t, m, input, manifest)
	previous := first
	for i := 0; i < 3; i++ {
		next := ExecutionPredecessor{JobID: domain.NewID(), ExecutionID: domain.NewID()}
		lease, err := m.ClaimContinuation(context.Background(), next.JobID, next.ExecutionID, previous, input, manifest)
		if err != nil {
			t.Fatal(err)
		}
		if err = lease.Close(); err != nil {
			t.Fatal(err)
		}
		history, err := security.ReadPrivate(m.executionHistoryPath(input.SessionID, previous.ExecutionID), 4096)
		if err != nil || strings.Contains(string(history), manifest.PrimaryPath) {
			t.Fatal("missing metadata-only predecessor evidence")
		}
		previous = next
	}
	if _, err := m.ClaimContinuation(context.Background(), domain.NewID(), first.ExecutionID, previous, input, manifest); err == nil {
		t.Fatal("retired execution ID was reused with a new process owner")
	}
	if _, err := m.ClaimContinuation(context.Background(), first.JobID, domain.NewID(), previous, input, manifest); err == nil {
		t.Fatal("retired process owner was silently adopted")
	}
}

func TestContinuationClaimsSerializeAcrossManagers(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	previous := closeFirstExecution(t, m, input, manifest)
	managers := []*Manager{m, {Root: m.Root, Logger: m.Logger}}
	var wg sync.WaitGroup
	leases := make([]*ExecutionLease, 2)
	errs := make([]error, 2)
	for i := range managers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			leases[i], errs[i] = managers[i].ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
		}(i)
	}
	wg.Wait()
	if (errs[0] == nil) == (errs[1] == nil) {
		t.Fatalf("competing continuation owners did not serialize: %v", errs)
	}
	for _, lease := range leases {
		if lease != nil {
			if err := lease.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestContinuationHistorySurvivesMissingLatestClaim(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	previous := closeFirstExecution(t, m, input, manifest)
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err = lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(m.executionClaimPath(input.SessionID)); err != nil {
		t.Fatal(err)
	}
	if _, err = m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest); err == nil {
		t.Fatal("losing latest ownership erased retained execution history")
	}
	if _, err = os.Stat(m.executionClaimPath(input.SessionID)); !os.IsNotExist(err) {
		t.Fatal("missing current ownership was silently replaced")
	}
}
