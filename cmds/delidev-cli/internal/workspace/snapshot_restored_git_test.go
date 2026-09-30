// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRestoredGitIdentityAtLongPrivatePath(t *testing.T) {
	m := manager(t)
	source, err := filepath.EvalSymlinks(repository(t))
	if err != nil {
		t.Fatal(err)
	}
	prepare, repoID := requestFor(source)
	base, err := os.MkdirTemp("", "dd-restore-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(base) })
	root, err := filepath.EvalSymlinks(base)
	if err != nil {
		t.Fatal(err)
	}
	// Keep CreateProcess cwd supported while object paths exceed MAX_PATH.
	rootLength := 230 - 1 - len(filepath.Join("workspaces", string(prepare.SessionID), string(repoID)))
	if len(root)+1 >= rootLength {
		t.Fatal("temporary root cannot fit the bounded long-path fixture")
	}
	root = filepath.Join(root, strings.Repeat("x", rootLength-len(root)-1))
	m.Root = root
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	repo := manifest.Repositories[0]
	object := filepath.Join(repo.Path, ".git", "objects", repo.BaseCommit[:2], repo.BaseCommit[2:])
	if len(repo.Path) > 250 || len(object) <= 280 {
		t.Fatal("fixture does not separate cwd and object path limits")
	}
	gitTest(t, source, "config", "core.longpaths", "false")
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	cleaned := storageDo(t, m, input)
	input.Action, input.OperationID, input.PreviousState, input.PreviousSnapshotID, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, input.SnapshotID, cleaned.Snapshot.SHA256
	storageDo(t, m, input)
	if _, err := m.verifyWorkspaceIdentity(context.Background(), prepare, manifest, continuationIdentity); err != nil {
		t.Fatal("restored Git identity requires ambient long-path configuration", err)
	}
	recovered := storageDo(t, m, recoveryRequest(input))
	if recovered.RecoveredJobState != domain.JobSucceeded {
		t.Fatal("restored publication could not be recovered")
	}
	work := domain.SessionDeletionWork{SessionID: prepare.SessionID, MachineID: prepare.MachineID, Copies: []domain.SessionDeletionCopy{{JobID: input.OperationID, SnapshotID: input.SnapshotID, Type: domain.WorkspaceStorageJob}}}
	if restored, err := m.deletionRestoredWorkspace(context.Background(), work, manifest); err != nil || !restored {
		t.Fatal("restored long-path identity cannot participate in deletion", err)
	}
	if actual := gitTest(t, source, "config", "core.longpaths"); actual != "false" {
		t.Fatal("source Git configuration was changed", actual)
	}
}
