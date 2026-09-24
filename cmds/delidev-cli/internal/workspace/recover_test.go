package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
)

func recoveryInput(request PrepareRequest) RecoveryRequest {
	return RecoveryRequest{JobID: domain.NewID(), InstanceID: domain.NewID(), Revision: 2, AssignmentDigest: strings.Repeat("a", 64), Preparation: request, Action: InspectPreparation}
}
func TestRecoveryPreservesReadyWorktreeAndRejectsChangedNativeOwnership(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := requestFor(root)
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	retained := filepath.Join(manifest.PrimaryPath, "ignored.txt")
	if err := os.WriteFile(retained, []byte("preserve modified tree"), 0600); err != nil {
		t.Fatal(err)
	}
	input := recoveryInput(request)
	input.Action = CleanupPreparation
	recovered, err := m.Recover(context.Background(), input, false)
	if err != nil || recovered.Outcome != RecoveredReady || recovered.Manifest.PrimaryPath != manifest.PrimaryPath {
		t.Fatalf("ready recovery: %+v %v", recovered, err)
	}
	if raw, err := os.ReadFile(retained); err != nil || string(raw) != "preserve modified tree" {
		t.Fatal("cleanup removed a ready workspace")
	}
	gitTest(t, manifest.PrimaryPath, "add", "ignored.txt")
	gitTest(t, manifest.PrimaryPath, "commit", "-m", "unexpected post-preparation commit")
	if _, err := m.Recover(context.Background(), input, false); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("changed preparation commit was accepted")
	}
	if _, err := os.Stat(retained); err != nil {
		t.Fatal("uncertainty removed retained content")
	}
}
func TestExplicitPartialRecoveryKeepsDurableCleanupProof(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := requestFor(root)
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = CleanupPending
	raw, _ := json.Marshal(manifest)
	path := filepath.Join(m.Root, "workspaces", string(request.SessionID), "manifest.json")
	if err := security.WriteAtomic(path, raw); err != nil {
		t.Fatal(err)
	}
	input := recoveryInput(request)
	if _, err := m.Recover(context.Background(), input, false); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("inspection implicitly deleted incomplete preparation")
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("inspection removed files")
	}
	input.Action = CleanupPreparation
	result, err := m.Recover(context.Background(), input, false)
	if err != nil || result.Outcome != RecoveredClean {
		t.Fatalf("cleanup failed: %+v %v", result, err)
	}
	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Fatal("partial root remains")
	}
	if entries := gitTest(t, root, "worktree", "list", "--porcelain"); strings.Contains(entries, manifest.PrimaryPath) {
		t.Fatal("cleanup left Git registration")
	}
	result, err = m.Recover(context.Background(), input, false)
	if err != nil || result.Outcome != RecoveredClean {
		t.Fatal("completed cleanup lost its proof", err)
	}
	// Model a crash after removal but before publishing the final marker.
	proofPath := filepath.Join(m.Root, "workspace-recovery", string(input.JobID)+".json")
	raw, err = security.ReadPrivate(proofPath, 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	var proof cleanupProof
	if err := domain.Decode(raw, &proof); err != nil {
		t.Fatal(err)
	}
	proof.Complete = false
	if err := writeCleanupProof(proofPath, proof); err != nil {
		t.Fatal(err)
	}
	result, err = m.Recover(context.Background(), input, false)
	if err != nil || result.Outcome != RecoveredClean {
		t.Fatal("interrupted post-removal publication could not recover", err)
	}
	if gitTest(t, root, "branch", "--show-current") != "main" {
		t.Fatal("cleanup changed original checkout")
	}
	input.JobID = domain.NewID()
	if _, err := m.Recover(context.Background(), input, false); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("another job reused cleanup proof")
	}
}
func TestRecoveryRejectsPartialOwnershipMismatchWithoutDeletion(t *testing.T) {
	m := manager(t)
	request := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = CleanupPending
	manifest.MachineID = domain.NewID()
	raw, _ := json.Marshal(manifest)
	if err := security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(request.SessionID), "manifest.json"), raw); err != nil {
		t.Fatal(err)
	}
	input := recoveryInput(request)
	input.Action = CleanupPreparation
	if _, err := m.Recover(context.Background(), input, false); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign partial manifest accepted")
	}
	if _, err := os.Stat(manifest.PrimaryPath); err != nil {
		t.Fatal("foreign manifest caused deletion")
	}
}

func TestPartialRecoveryRejectsReplacementLinkBeforeGitRemoval(t *testing.T) {
	root, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	request, _ := requestFor(root)
	m := manager(t)
	manifest, err := m.Prepare(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	manifest.State = CleanupPending
	raw, _ := json.Marshal(manifest)
	if err := security.WriteAtomic(filepath.Join(m.Root, "workspaces", string(request.SessionID), "manifest.json"), raw); err != nil {
		t.Fatal(err)
	}
	parked := filepath.Join(t.TempDir(), "original-owned-worktree")
	if err := os.Rename(manifest.PrimaryPath, parked); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, manifest.PrimaryPath); err != nil {
		t.Skip("replacement link fixture unavailable on this platform")
	}
	input := recoveryInput(request)
	input.Action = CleanupPreparation
	if _, err := m.Recover(context.Background(), input, false); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement link reached native Git removal")
	}
	if _, err := os.Stat(filepath.Join(root, "tracked.txt")); err != nil {
		t.Fatal("recovery changed original checkout")
	}
	if _, err := os.Stat(filepath.Join(parked, "tracked.txt")); err != nil {
		t.Fatal("recovery removed parked owned content")
	}
}
