// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRemoveClaimedStorageRemovalRejectsForeignNamespaceBytes(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	removal := filepath.Join(m.Root, "workspace-removals", string(input.OperationID))
	m.storageBeforeRemovalUnlink = func(relative string) {
		if relative == "chat/keep" {
			if err := os.WriteFile(filepath.Join(removal, "foreign"), []byte("preserve"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := m.Storage(context.Background(), input); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("foreign namespace bytes did not retain recovery", err)
	}
	m.storageBeforeRemovalUnlink = nil
	if err := m.RemoveClaimedStorageRemoval(context.Background(), input.OperationID, input.Preparation.SessionID, input.SnapshotID); domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("generic namespace replacement was accepted", err)
	}
	if raw, err := os.ReadFile(filepath.Join(removal, "foreign")); err != nil || string(raw) != "preserve" {
		t.Fatal("foreign namespace bytes were removed", err)
	}
}
