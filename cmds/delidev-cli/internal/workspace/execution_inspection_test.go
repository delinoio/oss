package workspace

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func TestClosedExecutionInspectionHoldsOwnershipAndPreservesAgentResults(t *testing.T) {
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
	gitTest(t, manifest.PrimaryPath, "checkout", "-b", "inspection-results")
	result := filepath.Join(manifest.PrimaryPath, "result.txt")
	if err := os.WriteFile(result, []byte("retained agent result"), 0600); err != nil {
		t.Fatal(err)
	}
	gitTest(t, manifest.PrimaryPath, "add", "result.txt")
	gitTest(t, manifest.PrimaryPath, "commit", "-m", "agent result")
	head := gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD")
	if err := os.WriteFile(result, []byte("later dirty result"), 0600); err != nil {
		t.Fatal(err)
	}
	inspection, err := m.InspectClosedExecution(context.Background(), previous, input, manifest)
	if err != nil || inspection.WorkingDirectory() != manifest.PrimaryPath {
		t.Fatal("existing closed worktree was not inspectable", err)
	}
	t.Cleanup(func() { _ = inspection.Close() })
	if _, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest); err == nil {
		t.Fatal("continuation displaced an active inspection")
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := security.ReadPrivate(m.executionClaimPath(input.SessionID), 4096)
	if err != nil || !bytes.Equal(before, after) || gitTest(t, manifest.PrimaryPath, "rev-parse", "HEAD") != head {
		t.Fatal("inspection rewrote cleanup ownership or agent commits", err)
	}
	raw, err := os.ReadFile(result)
	if err != nil || string(raw) != "later dirty result" {
		t.Fatal("inspection changed dirty agent results", err)
	}
	lease, err := m.ClaimContinuation(context.Background(), domain.NewID(), domain.NewID(), previous, input, manifest)
	if err != nil {
		t.Fatal("inspection retained its lock", err)
	}
	if err := lease.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InspectClosedExecution(context.Background(), previous, input, manifest); err == nil {
		t.Fatal("retired original owner substituted for the current execution")
	}
}

func TestClosedExecutionInspectionNeverRecreatesMissingProcessEvidence(t *testing.T) {
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
	index := filepath.Join(m.Git.ProcessRoot, string(input.SessionID))
	if err := os.RemoveAll(index); err != nil {
		t.Fatal(err)
	}
	if _, err := m.InspectClosedExecution(context.Background(), previous, input, manifest); err == nil {
		t.Fatal("new Git inspection replaced missing preparation-process proof")
	}
	if _, err := os.Lstat(index); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("inspection reconstructed the missing owner index", err)
	}
}
