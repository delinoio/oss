// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestSnapshotAdmissionReservesPrivatePathHeadroom(t *testing.T) {
	// A short original spelling can grow at every retained ancestor claim.
	if snapshotRemovalPathFits(strings.TrimSuffix(strings.Repeat("a/", 90), "/"), 4096) {
		t.Fatal("short source path did not reserve private ancestor spellings")
	}
	m := manager(t)
	prepare := PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), OriginMachineID: domain.NewID(), Type: domain.GeneralChat, Repositories: []RepositorySpec{}}
	prepare.OriginMachineID = prepare.MachineID
	manifest, err := m.Prepare(context.Background(), prepare)
	if err != nil {
		t.Fatal(err)
	}
	path := manifest.PrimaryPath
	for i := 0; i < 90; i++ {
		path = filepath.Join(path, "a")
	}
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(path, "payload")
	if err := os.WriteFile(file, []byte("original deep source"), 0600); err != nil {
		t.Fatal(err)
	}
	r := StorageRequest{Version: 1, OperationID: domain.NewID(), Action: StoragePreview, PreviousState: domain.WorkspacePresent, Preparation: prepare, Manifest: manifest}
	result, err := m.Storage(context.Background(), r)
	if domain.SafeError(err).Code != domain.ResourceExhausted || result.PreviewDigest != "" || result.Snapshot != nil {
		t.Fatal("insufficient rename headroom became removal authority", result, err)
	}
	if data, err := os.ReadFile(file); err != nil || string(data) != "original deep source" {
		t.Fatal("headroom rejection changed source", err)
	}
	if _, err := os.Lstat(m.removalClaimPath(r.OperationID)); !os.IsNotExist(err) {
		t.Fatal("headroom rejection claimed source removal", err)
	}
}

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

func TestSnapshotObservationSharesBudgetBeforeHashing(t *testing.T) {
	for _, limit := range []string{"entries", "bytes"} {
		t.Run(limit, func(t *testing.T) {
			budget := snapshotCopyBudget{bytes: 9, entries: 2}
			if limit == "bytes" {
				budget.entries = 20
			}
			for i := 0; i < 3; i++ {
				source := t.TempDir()
				path := filepath.Join(source, "payload")
				if err := os.WriteFile(path, []byte("four"), 0600); err != nil {
					t.Fatal(err)
				}
				inventory, err := walkSnapshotBudget(context.Background(), source, "", nil, MaxSnapshotEntries, &budget)
				if i < 2 {
					if err != nil || inventory.Bytes != 4 || len(inventory.Entries) != 1 {
						t.Fatal("bounded observation failed", inventory, err)
					}
				} else if domain.SafeError(err).Code != domain.ResourceExhausted || len(inventory.Entries) != 0 {
					t.Fatal("aggregate observation admitted an excess payload", inventory, err)
				}
				if data, err := os.ReadFile(path); err != nil || string(data) != "four" {
					t.Fatal("observation changed source", err)
				}
			}
		})
	}
}

func TestSnapshotObservationStopsAtAggregateGitInventory(t *testing.T) {
	m := manager(t)
	input, sources := snapshotRequest(t, m, true)
	original := make(map[string]string)
	for _, source := range sources {
		data, err := os.ReadFile(filepath.Join(source, "tracked.txt"))
		if err != nil {
			t.Fatal(err)
		}
		original[source] = string(data)
	}
	git := m.Git
	git.OwnerID, git.readOnly, git.offline = input.Preparation.SessionID, true, true
	for _, repo := range input.Manifest.Repositories {
		fields, err := git.revParseFields(context.Background(), repo.Path, 2, "--git-common-dir", "--absolute-git-dir")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < MaxSnapshotEntries/2; i++ {
			if err := os.WriteFile(filepath.Join(fields[0], "info", fmt.Sprintf("observation-%04d", i)), nil, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	value, err := m.storageObservation(context.Background(), input)
	if domain.SafeError(err).Code != domain.ResourceExhausted || value.Digest != "" || len(value.Data.Entries) != 0 {
		t.Fatal("aggregate Git observation returned partial authority", err)
	}
	result, err := m.Storage(context.Background(), input)
	if domain.SafeError(err).Code != domain.ResourceExhausted || result.PreviewDigest != "" || result.Snapshot != nil {
		t.Fatal("oversized Git observation became a preview", result, err)
	}
	for _, source := range sources {
		if data, err := os.ReadFile(filepath.Join(source, "tracked.txt")); err != nil || string(data) != original[source] {
			t.Fatal("oversized observation changed original source", err)
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
