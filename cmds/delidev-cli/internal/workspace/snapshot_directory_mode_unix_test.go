// SPDX-License-Identifier: Apache-2.0
//go:build !windows

package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestRemovalRecoveryRequiresPinnedOrJournaledDirectoryMode(t *testing.T) {
	for _, changed := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-transition", true: "retained-writer"}[changed], func(t *testing.T) {
			m, prepare, manifest := chatExecutionFixture(t)
			captureStorageFailureLogs(t, m)
			source := filepath.Join(manifest.PrimaryPath, "protected")
			if err := os.Mkdir(source, 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(source, "keep"), []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			held, err := os.OpenRoot(source)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
			if err := held.Chmod(".", 0500); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { held.Chmod(".", 0700) })
			input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
			preview := storageDo(t, m, input)
			input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
			t.Cleanup(func() {
				os.Chmod(filepath.Join(m.snapshotPath(input.SnapshotID), "workspace", "chat", "protected"), 0700)
			})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			interrupted := false
			m.storageBeforeRemovalUnlink = func(name string) {
				if name == "chat/protected/keep" {
					interrupted = true
					cancel()
				}
			}
			result, err := m.Storage(ctx, input)
			if !interrupted || domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
				t.Fatal("interrupted cleanup lost recovery", err)
			}
			intent, err := os.ReadFile(m.removalIntentPath(input.OperationID))
			if err != nil {
				t.Fatal(err)
			}
			_, pending, err := m.readRemovalClaimPending(input, intent)
			if err != nil {
				t.Fatal(err)
			}
			proof := false
			for _, p := range pending {
				proof = proof || p.Original == "chat/protected" && p.ModePrepared && p.Renamed
			}
			if !proof {
				t.Fatal("native directory mode lacks original journal proof")
			}
			if err := m.compactRemovalClaimJournal(context.Background(), input); err != nil {
				t.Fatal(err)
			}
			if changed {
				if err := held.Chmod(".", 0750); err != nil {
					t.Fatal(err)
				}
			}
			m = &Manager{Root: m.Root, Logger: m.Logger}
			recovered, err := m.Storage(context.Background(), recoveryRequest(input))
			if !changed {
				if err != nil || !recovered.CleanupVerified || recovered.RecoveredJobState != domain.JobSucceeded {
					t.Fatal("journaled read-only transition did not recover", err)
				}
				return
			}
			if domain.SafeError(err).Code != domain.RecoveryRequired || recovered.CleanupVerified || recovered.RemovedSourceBytes != 0 {
				t.Fatal("recovery adopted writer permissions", err)
			}
			if info, err := held.Lstat("."); err != nil || info.Mode().Perm() != 0750 {
				t.Fatal("writer permissions changed", err)
			}
			if raw, err := held.ReadFile("keep"); err != nil || string(raw) != "original" {
				t.Fatal("writer directory content removed", err)
			}
		})
	}
}

func TestClaimedRemovalPreservesPostClaimDirectoryPermissionWrite(t *testing.T) {
	m, prepare, manifest := chatExecutionFixture(t)
	captureStorageFailureLogs(t, m)
	source := filepath.Join(manifest.PrimaryPath, "protected")
	if err := os.Mkdir(source, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "keep"), []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	held, err := os.OpenRoot(source)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	input := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	preview := storageDo(t, m, input)
	input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = StorageCleanup, domain.NewID(), domain.NewID(), preview.PreviewDigest
	changed := false
	m.storageAfterRemovalClaim = func(name string) {
		if name == "chat/protected/keep" {
			changed = true
			if err := held.Chmod(".", 0750); err != nil {
				t.Fatal(err)
			}
		}
	}
	result, err := m.Storage(context.Background(), input)
	if !changed || domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
		t.Fatal("permission write was silently removed", err)
	}
	if info, err := held.Lstat("."); err != nil || info.Mode().Perm() != 0750 {
		t.Fatal("late permission write changed", err)
	}
	m = &Manager{Root: m.Root, Logger: m.Logger}
	if result, err := m.Storage(context.Background(), recoveryRequest(input)); domain.SafeError(err).Code != domain.RecoveryRequired || result.CleanupVerified {
		t.Fatal("recovery adopted late permission write", err)
	}
}
