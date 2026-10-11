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
