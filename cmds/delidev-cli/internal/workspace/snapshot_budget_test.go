// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotCopySharesByteBudgetBeforeWriting(t *testing.T) {
	budget := snapshotCopyBudget{bytes: 9, entries: 20}
	destination := t.TempDir()
	for i := 0; i < 3; i++ {
		source := t.TempDir()
		if err := os.WriteFile(filepath.Join(source, "payload"), []byte("four"), 0600); err != nil {
			t.Fatal(err)
		}
		target := filepath.Join(destination, fmt.Sprint(i))
		_, err := walkSnapshotBudget(context.Background(), source, target, nil, MaxSnapshotEntries, &budget)
		if i < 2 {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if domain.SafeError(err).Code != domain.ResourceExhausted {
				t.Fatal("aggregate copy did not stop", err)
			}
			if _, err := os.Lstat(filepath.Join(target, "payload")); !os.IsNotExist(err) {
				t.Fatal("over-budget file was created", err)
			}
		}
		if raw, err := os.ReadFile(filepath.Join(source, "payload")); err != nil || string(raw) != "four" {
			t.Fatal("source changed", err)
		}
	}
}

func TestSnapshotCopyOverlayCountsOnlyNewDirectories(t *testing.T) {
	target := filepath.Join(t.TempDir(), "copy")
	budget := snapshotCopyBudget{bytes: 4, entries: 3}
	for i := 0; i < 2; i++ {
		source := t.TempDir()
		if err := os.Mkdir(filepath.Join(source, "logs"), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(source, "logs", fmt.Sprint(i)), []byte("ok"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := walkSnapshotBudget(context.Background(), source, target, nil, MaxSnapshotEntries, &budget, i > 0); err != nil {
			t.Fatal(err)
		}
	}
	if budget.entries != 0 || budget.bytes != 0 {
		t.Fatal("overlay did not consume exact complete inventory", budget)
	}
}

func TestSnapshotCreateStopsAtAggregateRepositoryEntryBudget(t *testing.T) {
	m := manager(t)
	input, sources := snapshotRequest(t, m, true)
	// Measure independently transformed Git stores rather than relying on a Git
	// version's template inventory. Workspace + first store exactly fill capacity;
	// the second store must fail before creating its administration root.
	firstCopy, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := m.copySnapshotGit(context.Background(), input.Preparation.SessionID, input.Manifest.Repositories[0], firstCopy); err != nil {
		t.Fatal(err)
	}
	first, err := walkSnapshot(context.Background(), firstCopy, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	observed, err := m.storageObservation(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	count := MaxSnapshotEntries - len(observed.Data.Entries) - len(first.Entries)
	for i := 0; i < count; i++ {
		if err := os.WriteFile(filepath.Join(input.Manifest.Repositories[0].Path, fmt.Sprintf("budget-%04d", i)), nil, 0600); err != nil {
			t.Fatal(err)
		}
	}
	input.Action, input.SnapshotID = StorageCreate, domain.NewID()
	result, err := m.Storage(context.Background(), input)
	if domain.SafeError(err).Code != domain.ResourceExhausted || result.Snapshot != nil {
		t.Fatal("aggregate creation was not rejected", result, err)
	}
	for _, path := range []string{m.snapshotPath(input.SnapshotID), filepath.Join(m.Root, "snapshot-staging", string(input.OperationID))} {
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("rejected capture retained output", err)
		}
	}
	for _, repo := range input.Manifest.Repositories {
		if _, err := os.Stat(repo.Path); err != nil {
			t.Fatal("source workspace removed", err)
		}
	}
	for _, source := range sources {
		if _, err := os.Stat(source); err != nil {
			t.Fatal("original source removed", err)
		}
	}
}
