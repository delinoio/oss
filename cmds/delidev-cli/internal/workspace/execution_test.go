package workspace

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func chatExecutionFixture(t *testing.T) (*Manager, PrepareRequest, Manifest) {
	t.Helper()
	m := manager(t)
	input := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	manifest, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return m, input, manifest
}

func TestExecutionWorkspaceClaimSerializesPreparationAndKeepsFiles(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	job, execution := domain.NewID(), domain.NewID()
	lease, err := m.ClaimFirstExecution(context.Background(), job, execution, input, manifest)
	if err != nil || lease.WorkingDirectory() != manifest.PrimaryPath {
		t.Fatalf("claim failed: %v", err)
	}
	t.Cleanup(func() { _ = lease.Close() })
	if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest); err == nil {
		t.Fatal("concurrent native execution acquired the same workspace")
	}
	if _, err := m.Prepare(context.Background(), input); err == nil {
		t.Fatal("preparation crossed active native ownership")
	}
	if _, err := m.Recover(context.Background(), recoveryInput(input), false); err == nil {
		t.Fatal("recovery crossed active native ownership")
	}
	retained := filepath.Join(lease.WorkingDirectory(), "keep.txt")
	if err := os.WriteFile(retained, []byte("retained user content"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal("lease close is not idempotent", err)
	}
	claim, err := m.readExecutionClaim(input.SessionID)
	if err != nil || claim.State != executionClaimClosed || claim.JobID != job || claim.ExecutionID != execution {
		t.Fatal("cleanup proof lost native ownership")
	}
	if raw, err := os.ReadFile(retained); err != nil || string(raw) != "retained user content" {
		t.Fatal("lease completion removed workspace content")
	}
	if _, err := m.ClaimFirstExecution(context.Background(), job, execution, input, manifest); err == nil || domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("closed first execution was replayed")
	}
	if _, err := m.Prepare(context.Background(), input); err != nil {
		t.Fatal("closed lease retained a live lock", err)
	}
}

func TestExecutionClaimSurvivesWorkerLossAndMissingWorkspace(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate OS lock release after a Worker exit without a native completion.
	// Do not call Close: doing so would create the cleanup proof under test.
	if err := lease.release(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Dir(manifest.PrimaryPath)); err != nil {
		t.Fatal(err)
	}
	replacement := &Manager{Root: m.Root, Logger: m.Logger}
	if _, err := replacement.Prepare(context.Background(), input); err == nil {
		t.Fatal("missing workspace erased uncertain execution")
	}
	if _, err := replacement.Recover(context.Background(), recoveryInput(input), true); err == nil {
		t.Fatal("old preparation cleanup proof cleared active execution")
	}
	if _, err := replacement.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest); err == nil {
		t.Fatal("replacement Worker started over uncertain native ownership")
	}
	claim, err := replacement.readExecutionClaim(input.SessionID)
	if err != nil || claim.State != executionClaimActive {
		t.Fatal("execution uncertainty was removed")
	}
}

func TestExecutionLeaseCannotClaimCleanupFromMissingProcessIndex(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	job := domain.NewID()
	lease, err := m.ClaimFirstExecution(context.Background(), job, domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(m.Git.ProcessRoot, string(job))); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("missing native process evidence became cleanup proof")
	}
	claim, err := m.readExecutionClaim(input.SessionID)
	if err != nil || claim.State != executionClaimActive {
		t.Fatal("uncertain cleanup cleared the durable claim")
	}
	if _, err := m.Prepare(context.Background(), input); err == nil {
		t.Fatal("uncertain cleanup authorized workspace preparation")
	}
}

func TestExecutionClaimRequiresExactReadyManifestAndNativeWorktree(t *testing.T) {
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
	changed := manifest
	changed.CreatedAt = changed.CreatedAt.Add(1)
	if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, changed); err == nil {
		t.Fatal("a different published manifest acquired execution")
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "dirty.txt"), []byte("keep this change"), 0600); err != nil {
		t.Fatal(err)
	}
	lease, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal("dirty prepared worktree was rejected or reset", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if got := gitTest(t, root, "branch", "--show-current"); got != "main" {
		t.Fatal("source checkout changed")
	}
	// A separate never-executed workspace must still have the recorded detached
	// creation commit; do not silently adopt another HEAD at the first claim.
	input.SessionID = domain.NewID()
	other, err := m.Prepare(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	gitTest(t, other.PrimaryPath, "checkout", "-b", "changed-head")
	if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, other); err == nil {
		t.Fatal("attached worktree was silently adopted")
	}
	if _, err := m.readExecutionClaim(input.SessionID); !os.IsNotExist(err) {
		t.Fatal("failed preflight consumed the first execution claim")
	}
}

func TestExecutionClaimRejectsCanceledForgedAndExistingOwnerScopes(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := m.ClaimFirstExecution(canceled, domain.NewID(), domain.NewID(), input, manifest); err == nil || domain.SafeError(err).Code != domain.Canceled {
		t.Fatal("pre-claim cancellation was lost")
	}
	forged := manifest
	forged.PrimaryPath = filepath.Join(t.TempDir(), "workspaces", string(input.SessionID), "chat")
	if err := os.MkdirAll(forged.PrimaryPath, 0700); err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(forged)
	if err := security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(input.SessionID), "manifest.json"), raw); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimFirstExecution(context.Background(), domain.NewID(), domain.NewID(), input, forged); err == nil {
		t.Fatal("another workspace root with the same UUID suffix was adopted")
	}
	raw, _ = json.Marshal(manifest)
	if err := security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(input.SessionID), "manifest.json"), raw); err != nil {
		t.Fatal(err)
	}
	job := domain.NewID()
	if err := security.PrivateDir(filepath.Join(m.Git.ProcessRoot, string(job))); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ClaimFirstExecution(context.Background(), job, domain.NewID(), input, manifest); err == nil {
		t.Fatal("existing execution process scope was adopted")
	}
}

func TestWorkspaceExecutionProcessFixture(t *testing.T) {
	if os.Getenv("DELIDEV_WORKSPACE_EXECUTION_FIXTURE") != "1" {
		return
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
}

func TestExecutionLeaseChecksRealOwnedProcessCleanup(t *testing.T) {
	m, input, manifest := chatExecutionFixture(t)
	job := domain.NewID()
	lease, err := m.ClaimFirstExecution(context.Background(), job, domain.NewID(), input, manifest)
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if err := process.Run(context.Background(), process.Config{Directory: m.Git.ProcessRoot, OwnerID: job, Executable: executable, Args: []string{"-test.run=^TestWorkspaceExecutionProcessFixture$"}, Env: []string{"DELIDEV_WORKSPACE_EXECUTION_FIXTURE=1"}, Cwd: lease.WorkingDirectory(), Stdout: io.Discard, Stderr: io.Discard, Logger: m.Logger}); err != nil {
		t.Fatal(err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := security.ReadPrivate(m.executionClaimPath(input.SessionID), 4096)
	if err != nil || strings.Contains(string(raw), "FIXTURE") || strings.Contains(string(raw), manifest.PrimaryPath) {
		t.Fatal("execution ownership journal contains command/environment/path data")
	}
}
