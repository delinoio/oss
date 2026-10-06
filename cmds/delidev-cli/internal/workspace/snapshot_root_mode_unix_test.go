// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestSnapshotCleanupRejectsChangedManagedRootBeforeRename(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	captureStorageFailureLogs(t, m)
	root := filepath.Join(m.Root, "workspaces", string(prepare.SessionID))
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	before, err := walkSnapshot(context.Background(), root, "", nil)
	if err != nil || before.RootMode == 0 {
		t.Fatal("root mode was not retained", err)
	}
	held, err := os.OpenRoot(root)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	defer held.Chmod(".", 0700)
	if err := held.Chmod(".", 0750); err != nil {
		t.Fatal(err)
	}
	after, err := walkSnapshot(context.Background(), root, "", nil)
	if err != nil || inventoryDigest(after) == inventoryDigest(before) {
		t.Fatal("root mode absent from inventory digest", err)
	}
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	if _, err := m.Storage(context.Background(), input); err == nil {
		t.Fatal("changed root accepted cleanup")
	}
	if info, err := os.Lstat(root); err != nil || info.Mode().Perm() != 0750 {
		t.Fatal("root renamed or mode changed", err)
	}
	if _, err := os.Lstat(m.snapshotPath(input.SnapshotID)); !os.IsNotExist(err) {
		t.Fatal("changed root published snapshot", err)
	}
	if _, err := os.Lstat(filepath.Join(m.Root, "workspace-removals", string(input.OperationID))); !os.IsNotExist(err) {
		t.Fatal("changed root was renamed", err)
	}
	if err := held.Chmod(".", 0700); err != nil {
		t.Fatal(err)
	}
	if output := storageDo(t, m, input); output.WorkspaceState != domain.WorkspaceStored || !output.CleanupVerified {
		t.Fatal("original valid root could not retry")
	}
}
