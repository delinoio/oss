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

func TestManagedWorkspacePreparationRequiresNegotiationBeforeFilesystemChanges(t *testing.T) {
	for _, kind := range []workspace.RepositorySourceKind{workspace.RemoteCloneSource, workspace.IndependentForkSource} {
		t.Run(string(kind), func(t *testing.T) {
			machine, session, repository := domain.NewID(), domain.NewID(), domain.NewID()
			input := workspace.PrepareRequest{SessionID: session, MachineID: machine, Type: domain.Worktree, PrimaryRepository: repository, Repositories: []workspace.RepositorySpec{{ID: repository, SourceKind: kind, RemoteURL: "https://example.invalid/project.git"}}}
			raw, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			root := filepath.Join(t.TempDir(), "worker")
			_, err = execute(context.Background(), Config{Root: root}, domain.NewID(), domain.Job{Type: domain.PrepareWorkspaceJob, MachineID: machine, Input: raw})
			if domain.SafeError(err).Code != domain.Unsupported {
				t.Fatal("unnegotiated preparation was not rejected", err)
			}
			if _, err := os.Lstat(root); !os.IsNotExist(err) {
				t.Fatal("unnegotiated preparation changed the filesystem", err)
			}
		})
	}
}
