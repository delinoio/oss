// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotNamespaceGateSerializesRemovalAndOtherSessionScans(t *testing.T) {
	m := manager(t)
	first := chatStorageRequest(t, m)
	created := storageDo(t, m, first)
	other := &Manager{Root: m.Root, Git: m.Git, Logger: m.Logger}
	second := chatStorageRequest(t, other)
	retained := storageDo(t, other, second)
	first.Action, first.OperationID, first.SnapshotDigest, first.SnapshotMetadata = StorageDelete, domain.NewID(), created.Snapshot.SHA256, created.Snapshot
	entered, release := make(chan struct{}), make(chan struct{})
	var enter, leave sync.Once
	unblock := func() { leave.Do(func() { close(release) }) }
	defer unblock()
	m.storageAfterRemovalClaim = func(string) { enter.Do(func() { close(entered); <-release }) }
	done := make(chan error, 1)
	pending := true
	defer func() {
		unblock()
		if pending {
			<-done
		}
	}()
	go func() { _, err := m.Storage(context.Background(), first); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		pending = false
		t.Fatal("removal did not acquire gate", err)
	case <-time.After(time.Minute):
		t.Fatal("removal did not reach claim")
	}
	if _, _, err := other.snapshotInventoryBytes(context.Background(), second.Preparation.SessionID); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("global scan raced removal", err)
	}
	second.Action, second.OperationID, second.SnapshotDigest, second.SnapshotMetadata = StorageInspect, domain.NewID(), retained.Snapshot.SHA256, retained.Snapshot
	result, err := other.Storage(context.Background(), second)
	if domain.SafeError(err).Code != domain.Conflict || result.CleanupVerified {
		t.Fatal("other session observed an unstable snapshot namespace", err)
	}
	work := domain.SessionDeletionWork{SessionID: second.Preparation.SessionID, MachineID: second.Preparation.MachineID, PreparationDigests: []string{second.Manifest.InputDigest}}
	copyRemoved := false
	if err := other.DeleteOwnedWorkspace(context.Background(), work, false, func() error { copyRemoved = true; return nil }); domain.SafeError(err).Code != domain.SessionDeletionPending().Code || copyRemoved {
		t.Fatal("permanent deletion bypassed namespace gate", err)
	}
	if raw, err := os.ReadFile(filepath.Join(second.Manifest.PrimaryPath, "keep")); err != nil || string(raw) != "owned source" {
		t.Fatal("gate contention changed live source", err)
	}
	unblock()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	pending = false
	if result := storageDo(t, other, second); !result.CleanupVerified || result.Snapshot == nil {
		t.Fatal("settled namespace could not be observed")
	}
}
