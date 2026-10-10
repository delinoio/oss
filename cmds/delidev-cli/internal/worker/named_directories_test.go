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

func TestNamedPreparationRequiresSeparateNegotiatedCapabilityBeforeSideEffects(t *testing.T) {
	id := domain.NewID()
	machine := domain.NewID()
	repo := domain.NewID()
	preparation := workspace.PrepareRequest{SessionID: id, MachineID: machine, Type: domain.Worktree, PrimaryRepository: repo, Repositories: []workspace.RepositorySpec{{ID: repo, DirectoryName: "oss", Checkout: "/must-not-be-inspected"}}}
	raw, _ := json.Marshal(preparation)
	root := t.TempDir()
	_, err := execute(context.Background(), Config{Root: root, remoteWorkspaceClone: true}, domain.NewID(), domain.Job{Type: domain.PrepareWorkspaceJob, MachineID: machine, Input: raw})
	if domain.SafeError(err).Code != domain.Unsupported {
		t.Fatal("legacy clone capability accepted names", err)
	}
	if _, err := os.Stat(filepath.Join(root, "workspaces")); !os.IsNotExist(err) {
		t.Fatal("unsupported request made side effects", err)
	}
}
