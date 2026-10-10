// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func removalSnapshotFixture(t *testing.T, directory bool) (string, string, RemovalSnapshot) {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "original")
	file := path
	if directory {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		file = filepath.Join(path, "content")
	}
	if err := os.WriteFile(file, []byte("original bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := ObserveRemovalRoot(context.Background(), root, path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := CaptureRemovalSnapshot(context.Background(), root, path, original)
	if err != nil {
		t.Fatal(err)
	}
	return root, path, snapshot
}

func TestRemovalSnapshotRejectsReplacementIdentity(t *testing.T) {
	for _, directory := range []bool{false, true} {
		t.Run(map[bool]string{false: "file", true: "directory"}[directory], func(t *testing.T) {
			root, path, snapshot := removalSnapshotFixture(t, directory)
			if err := os.Rename(path, path+"-retained"); err != nil {
				t.Fatal(err)
			}
			file := path
			if directory {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
				file = filepath.Join(path, "content")
			}
			if err := os.WriteFile(file, []byte("original bytes"), 0600); err != nil {
				t.Fatal(err)
			}
			for _, started := range []bool{false, true} {
				if err := RemoveSnapshotTree(context.Background(), root, snapshot, started); err == nil {
					t.Fatal("replacement adopted")
				}
				if raw, err := os.ReadFile(file); err != nil || string(raw) != "original bytes" {
					t.Fatal("replacement removed", err)
				}
			}
		})
	}
}

func TestRemovalSnapshotRejectsChangedBytesAndNewEntries(t *testing.T) {
	for _, mutation := range []string{"changed-bytes", "nested-replacement", "new-entry"} {
		t.Run(mutation, func(t *testing.T) {
			root, path, snapshot := removalSnapshotFixture(t, true)
			file := filepath.Join(path, "content")
			info, err := os.Stat(file)
			if err != nil {
				t.Fatal(err)
			}
			switch mutation {
			case "changed-bytes":
				if err := os.WriteFile(file, []byte("foreign bytes!"), 0600); err != nil {
					t.Fatal(err)
				}
				if err := os.Chtimes(file, info.ModTime(), info.ModTime()); err != nil {
					t.Fatal(err)
				}
			case "nested-replacement":
				if err := os.Rename(file, filepath.Join(root, "retained-content")); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(file, []byte("original bytes"), 0600); err != nil {
					t.Fatal(err)
				}
			case "new-entry":
				if err := os.WriteFile(filepath.Join(path, "foreign"), []byte("foreign"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if err := RemoveSnapshotTree(context.Background(), root, snapshot, true); err == nil {
				t.Fatal("mutated tree adopted")
			}
			if _, err := os.Stat(file); err != nil {
				t.Fatal("original entry removed before complete validation", err)
			}
		})
	}
}

func TestRemovalSnapshotRetainsEarlyRootAndAbsence(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "replaced-before-snapshot", true: "created-after-absence"}[absent], func(t *testing.T) {
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(root, "operand")
			if !absent {
				if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			original, err := ObserveRemovalRoot(context.Background(), root, path)
			if err != nil {
				t.Fatal(err)
			}
			if !absent {
				if err := os.Rename(path, path+"-retained"); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := CaptureRemovalSnapshot(context.Background(), root, path, original); err == nil {
				t.Fatal("new operand adopted")
			}
		})
	}
}

func TestRemovalSnapshotOriginalDeletionAndPartialReplay(t *testing.T) {
	root, path, snapshot := removalSnapshotFixture(t, true)
	if err := os.Remove(filepath.Join(path, "content")); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshotTree(context.Background(), root, snapshot, false); err == nil {
		t.Fatal("fresh intent accepted missing original entry")
	}
	if err := RemoveSnapshotTree(context.Background(), root, snapshot, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if err := RemoveSnapshotTree(context.Background(), root, snapshot, true); err != nil {
		t.Fatal(err)
	}
	root, path, snapshot = removalSnapshotFixture(t, true)
	if err := RemoveSnapshotTree(context.Background(), root, snapshot, false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
}

func TestRemovalSnapshotCancellationAndAbsentReplay(t *testing.T) {
	root, path, snapshot := removalSnapshotFixture(t, true)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := RemoveSnapshotTree(ctx, root, snapshot, true); err == nil {
		t.Fatal("cancellation ignored")
	}
	if _, err := os.Stat(filepath.Join(path, "content")); err != nil {
		t.Fatal(err)
	}
	absent := filepath.Join(root, "absent")
	original, err := ObserveRemovalRoot(context.Background(), root, absent)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := CaptureRemovalSnapshot(context.Background(), root, absent, original)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshotTree(context.Background(), root, plan, true); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absent, []byte("foreign"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveSnapshotTree(context.Background(), root, plan, true); err == nil {
		t.Fatal("absence adopted new file")
	}
}
