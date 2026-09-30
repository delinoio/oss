// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRestoredIdentityRejectsReplacedRepositoryAndGitStore(t *testing.T) {
	for _, replacement := range []string{"git-store", "repository"} {
		t.Run(replacement, func(t *testing.T) {
			m := manager(t)
			input, _ := snapshotRequest(t, m, false)
			preview := storageDo(t, m, input)
			input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			cleaned := storageDo(t, m, input)
			input.Action, input.OperationID, input.PreviousState, input.SnapshotDigest = StorageRestore, domain.NewID(), domain.WorkspaceStored, cleaned.Snapshot.SHA256
			storageDo(t, m, input)
			repo := input.Manifest.Repositories[0]
			// Ordinary user edits, index/config writes and commits keep directory
			// ownership. A full snapshot hash would incorrectly reject these.
			if err := os.WriteFile(filepath.Join(repo.Path, "tracked.txt"), []byte("ordinary new commit\n"), 0600); err != nil {
				t.Fatal(err)
			}
			gitTest(t, repo.Path, "add", "tracked.txt")
			gitTest(t, repo.Path, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-m", "ordinary commit")
			if _, err := m.verifyWorkspaceIdentity(context.Background(), input.Preparation, input.Manifest, continuationIdentity); err != nil {
				t.Fatal("ordinary commit lost restored identity", err)
			}
			path := repo.Path
			if replacement == "git-store" {
				path = filepath.Join(path, ".git")
			}
			// Keep the original inode alive and publish byte-identical replacement
			// directories. Paths and valid HEAD alone cannot establish identity.
			old := path + "-original"
			if err := os.Rename(path, old); err != nil {
				t.Fatal(err)
			}
			if _, err := walkSnapshot(context.Background(), old, path, nil); err != nil {
				t.Fatal(err)
			}
			if _, err := m.verifyWorkspaceIdentity(context.Background(), input.Preparation, input.Manifest, continuationIdentity); domain.SafeError(err).Code != domain.RecoveryRequired {
				t.Fatal("replacement borrowed published identity", err)
			}
			work := domain.SessionDeletionWork{SessionID: input.Preparation.SessionID, MachineID: input.Preparation.MachineID, Copies: []domain.SessionDeletionCopy{{JobID: input.OperationID, SnapshotID: input.SnapshotID, Type: domain.WorkspaceStorageJob}}}
			if _, err := m.deletionRestoredWorkspace(context.Background(), work, input.Manifest); err == nil {
				t.Fatal("replacement borrowed deletion authority")
			}
			if _, err := os.Lstat(old); err != nil {
				t.Fatal("original store was changed", err)
			}
		})
	}
}

func TestRestoredIdentityRejectsReplacedGeneralChatRoot(t *testing.T) {
	m, input := storedGeneralChat(t)
	storageDo(t, m, input)
	root := filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if _, err := walkSnapshot(context.Background(), root+"-original", root, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.verifyWorkspaceIdentity(context.Background(), input.Preparation, input.Manifest, continuationIdentity); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("matching General Chat replacement borrowed identity", err)
	}
	if _, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("matching replacement was adopted by recovery", err)
	}
}
