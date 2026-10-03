// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func chatStorageRequest(t *testing.T, m *Manager) StorageRequest {
	t.Helper()
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "keep"), []byte("owned source"), 0600); err != nil {
		t.Fatal(err)
	}
	return StorageRequest{PreviousState: domain.WorkspacePresent, Version: 1, OperationID: domain.NewID(), Action: StorageCreate, Preparation: prepare, Manifest: manifest, SnapshotID: domain.NewID()}
}

// Admission reads bounded retained metadata, not the contents of unrelated
// sessions. Seed headers from a verified native snapshot to exercise that count
// without claiming thousands of fully copied/native-verified snapshot trees.
func seedSnapshotCapacityHeaders(t *testing.T, m *Manager, count int) {
	t.Helper()
	input := chatStorageRequest(t, m)
	storageDo(t, m, input)
	template, _, err := m.inspectSnapshot(context.Background(), input.SnapshotID)
	if err != nil {
		t.Fatal(err)
	}
	if err := removeSnapshotTree(context.Background(), m.snapshotPath(input.SnapshotID)); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < count; i++ {
		template.ID, template.OperationID = domain.NewID(), domain.NewID()
		raw, err := json.Marshal(template)
		if err != nil {
			t.Fatal(err)
		}
		root := m.snapshotPath(template.ID)
		if err := os.Mkdir(root, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "snapshot.json"), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSnapshotCapacityRejectsCreateAndCleanupBeforeStaging(t *testing.T) {
	m := manager(t)
	input := chatStorageRequest(t, m)
	seedSnapshotCapacityHeaders(t, m, maxPublishedSnapshots)
	input.Action = StoragePreview
	preview := storageDo(t, m, input)
	for _, action := range []StorageAction{StorageCreate, StorageCleanup} {
		input.Action, input.OperationID, input.SnapshotID, input.PreviewDigest = action, domain.NewID(), domain.NewID(), preview.PreviewDigest
		result, err := m.Storage(context.Background(), input)
		if domain.SafeError(err).Code != domain.ResourceExhausted || result.Snapshot != nil {
			t.Fatal("full inventory admitted publication", result, err)
		}
		for _, path := range []string{m.snapshotPath(input.SnapshotID), filepath.Join(m.Root, "snapshot-staging", string(input.OperationID))} {
			if _, err := os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("capacity rejection wrote output", err)
			}
		}
	}
	if raw, err := os.ReadFile(filepath.Join(input.Manifest.PrimaryPath, "keep")); err != nil || string(raw) != "owned source" {
		t.Fatal("capacity rejection changed source", err)
	}
	_, count, err := m.snapshotInventoryBytes(context.Background(), input.Preparation.SessionID)
	if err != nil || count != maxPublishedSnapshots {
		t.Fatal("full inventory became unreadable", count, err)
	}
}

func TestSnapshotCapacitySerializesDifferentSessionPublishers(t *testing.T) {
	m := manager(t)
	first, _ := snapshotRequest(t, m, false)
	first.Action, first.SnapshotID = StorageCreate, domain.NewID()
	// Independent Manager instances use the same filesystem gate, as separate
	// Worker processes would; the sessions still have independent session locks.
	other := &Manager{Root: m.Root, Git: m.Git}
	second := chatStorageRequest(t, other)
	seedSnapshotCapacityHeaders(t, m, maxPublishedSnapshots-1)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	m.storageCopyFault = func(string) error { close(entered); <-release; return nil }
	type completed struct {
		result StorageResult
		err    error
	}
	done := make(chan completed, 1)
	pending := true
	defer func() {
		unblock()
		if pending {
			<-done
		}
	}()
	go func() { result, err := m.Storage(context.Background(), first); done <- completed{result, err} }()
	select {
	case <-entered:
	case outcome := <-done:
		pending = false
		t.Fatal("first publisher failed before holding its slot", outcome.err)
	case <-time.After(time.Minute):
		t.Fatal("publisher did not reach copy gate")
	}
	concurrent, err := other.Storage(context.Background(), second)
	if domain.SafeError(err).Code != domain.Conflict || concurrent.Snapshot != nil {
		t.Fatal("another session entered an occupied publication slot", concurrent, err)
	}
	unblock()
	outcome := <-done
	pending = false
	if outcome.err != nil || outcome.result.Snapshot == nil {
		t.Fatal("reserved publisher failed", outcome.err)
	}
	_, count, err := m.snapshotInventoryBytes(context.Background(), first.Preparation.SessionID)
	if err != nil || count != maxPublishedSnapshots {
		t.Fatal("concurrent sessions exceeded the bound", count, err)
	}
	second.OperationID, second.SnapshotID = domain.NewID(), domain.NewID()
	if _, err := other.Storage(context.Background(), second); domain.SafeError(err).Code != domain.ResourceExhausted {
		t.Fatal("final slot admitted another snapshot", err)
	}
	first.Action, first.OperationID, first.SnapshotDigest, first.SnapshotMetadata = StorageDelete, domain.NewID(), outcome.result.Snapshot.SHA256, outcome.result.Snapshot
	storageDo(t, m, first)
	second.OperationID, second.SnapshotID = domain.NewID(), domain.NewID()
	if result := storageDo(t, other, second); result.Snapshot == nil {
		t.Fatal("released capacity was not reusable")
	}
	_, count, err = other.snapshotInventoryBytes(context.Background(), second.Preparation.SessionID)
	if err != nil || count != maxPublishedSnapshots {
		t.Fatal("replacement publication lost the bound", count, err)
	}
}
