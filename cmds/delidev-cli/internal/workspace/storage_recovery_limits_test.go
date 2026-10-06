// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotRecoveryPreservesPinnedSourceBytes(t *testing.T) {
	for _, action := range []StorageAction{StorageInspect, StorageRestore, StorageDelete} {
		t.Run(string(action), func(t *testing.T) {
			m, input := storedGeneralChat(t)
			pinned, metadata, err := m.inspectSnapshot(context.Background(), input.SnapshotID)
			if err != nil || pinned.SourceBytes == 0 {
				t.Fatal("fixture lacks nonzero original source", err)
			}
			input.Action = action
			input.SnapshotMetadata = &metadata
			completed := storageDo(t, m, input)
			recovered := storageDo(t, m, recoveryRequest(input))
			if recovered.SourceBytes != pinned.SourceBytes || recovered.SourceBytes != completed.SourceBytes || recovered.RecoveredJobState != domain.JobSucceeded {
				t.Fatal("recovery lost pinned logical source size", recovered)
			}
			if action == StorageDelete && (recovered.Snapshot == nil || !recovered.Snapshot.Deleted) {
				t.Fatal("post-unlink count did not retain exact deleted snapshot")
			}
		})
	}
}

func TestSnapshotRestoreRecoveryRemovesOnlyClaimedPartialScratch(t *testing.T) {
	for _, replaced := range []bool{false, true} {
		t.Run(map[bool]string{false: "original", true: "replacement"}[replaced], func(t *testing.T) {
			m, input := storedGeneralChat(t)
			staging, err := m.createStorageStaging(input)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(staging, "partial"), []byte("interrupted copy"), 0600); err != nil {
				t.Fatal(err)
			}
			if replaced {
				if err := os.Rename(staging, staging+"-original"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(staging, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(staging, "foreign"), []byte("preserve"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			recovered, err := m.Storage(context.Background(), recoveryRequest(input))
			if replaced {
				if domain.SafeError(err).Code != domain.RecoveryRequired {
					t.Fatal("replacement borrowed staging cleanup authority", err)
				}
				if raw, err := os.ReadFile(filepath.Join(staging, "foreign")); err != nil || string(raw) != "preserve" {
					t.Fatal("replacement changed", err)
				}
				if _, err := os.Stat(staging + "-original"); err != nil {
					t.Fatal("original staging changed", err)
				}
			} else {
				if err != nil || recovered.RecoveredJobState != domain.JobFailed || recovered.WorkspaceState != domain.WorkspaceStored || !recovered.CleanupVerified {
					t.Fatal("partial restore could not settle original unpublished copy", recovered, err)
				}
				if _, err := os.Lstat(staging); !os.IsNotExist(err) {
					t.Fatal("partial scratch remained", err)
				}
			}
			if _, err := os.Lstat(filepath.Join(m.Root, "workspaces", string(input.Preparation.SessionID))); !os.IsNotExist(err) {
				t.Fatal("recovery published a workspace", err)
			}
			if _, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID); err != nil {
				t.Fatal("recovery changed retained snapshot", err)
			}
		})
	}
}
