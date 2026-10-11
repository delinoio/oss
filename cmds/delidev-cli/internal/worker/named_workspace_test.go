// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	"os"
	"path/filepath"
	"testing"
)

func TestNamedWorkspaceExecutionRejectsUnnegotiatedPeerWithoutSideEffects(t *testing.T) {
	root := t.TempDir()
	repo := domain.NewID()
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.Worktree, PrimaryRepository: repo, Repositories: []workspace.RepositorySpec{{ID: repo, DirectoryName: "oss", SourceKind: workspace.RemoteCloneSource, RemoteURL: "https://github.com/fixture/repo.git"}}}
	raw, _ := json.Marshal(input)
	_, err := execute(context.Background(), Config{Root: root, remoteWorkspaceClone: true}, domain.NewID(), domain.Job{Type: domain.PrepareWorkspaceJob, MachineID: input.MachineID, Input: raw})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("named preparation was not gated", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workspaces")); !os.IsNotExist(err) {
		t.Fatal("unnegotiated execution created workspace", err)
	}
}
