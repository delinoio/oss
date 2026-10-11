// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
)

func TestNamedWorkspaceWorkerRefusesUnnegotiatedBeforeSideEffects(t *testing.T) {
	repo := domain.NewID()
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree, PrimaryRepository: repo, Repositories: []workspace.RepositorySpec{{ID: repo, DirectoryName: "oss", SourceKind: workspace.RemoteCloneSource, RemoteURL: "https://github.com/fixture/repo.git"}}}
	raw, _ := json.Marshal(input)
	job := domain.Job{Type: domain.PrepareWorkspaceJob, MachineID: input.MachineID, Input: raw}
	root := filepath.Join(t.TempDir(), "uncreated-worker")
	if _, err := execute(context.Background(), Config{Root: root, remoteWorkspaceClone: true}, domain.NewID(), job); domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("Worker silently accepted unsupported named preparation", err)
	}
	if _, err := os.Lstat(root); !os.IsNotExist(err) {
		t.Fatal("unsupported Worker created state", err)
	}
	input.Repositories[0].DirectoryName = ""
	job.Input, _ = json.Marshal(input)
	if named, err := workspace.JobUsesNamedManagedDirectories(job); err != nil || named {
		t.Fatal("legacy omission requires new capability", err)
	}
}

func TestNamedManagedSessionDeletionPreservesIndependentCopiesAndSource(t *testing.T) {
	c, work, manifest, source := deletionWorkerFixture(t, domain.Worktree, "oss")
	if filepath.Base(manifest.PrimaryPath) != "oss" {
		t.Fatal("named deletion fixture lost accepted component")
	}
	manager := workspace.Manager{Root: c.Root, Logger: c.Logger}
	other, err := manager.Prepare(context.Background(), workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: work.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "untracked.txt"), []byte("owned session"), 0600); err != nil {
		t.Fatal(err)
	}
	proof, err := deleteSessionCopies(context.Background(), c, work)
	if err != nil || !proof.Complete {
		t.Fatal("named permanent deletion failed", proof, err)
	}
	if _, err := os.Stat(manifest.PrimaryPath); !os.IsNotExist(err) {
		t.Fatal("named owned checkout retained", err)
	}
	if _, err := os.Stat(other.PrimaryPath); err != nil {
		t.Fatal("independent session removed", err)
	}
	if body, err := os.ReadFile(filepath.Join(source, "tracked.txt")); err != nil || string(body) != "original" {
		t.Fatal("original local source changed", err)
	}
	// Simulate a crash after unlinks and before completion acknowledgement.
	proof.Complete = false
	if err := writeJSON(sessionDeletionPath(c.Root, work.SessionID), proof); err != nil {
		t.Fatal(err)
	}
	replayed, err := deleteSessionCopies(context.Background(), c, work)
	if err != nil || !replayed.Complete || replayed.ReportID != proof.ReportID {
		t.Fatal("named deletion replay lost original acknowledgement", err)
	}
}
