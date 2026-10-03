// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/windows"
)

func TestSnapshotWindowsPreservesForwardAndDanglingDirectoryLinks(t *testing.T) {
	source := t.TempDir()
	// The sorted walker encounters each link before its target. Directory kind
	// must also survive when the original target is absent throughout the copy.
	for _, name := range []string{"a-forward", "b-dangling"} {
		if err := createSnapshotSymlink("z-target", filepath.Join(source, name), snapshotDirectoryLink); err != nil {
			if errors.Is(err, windows.ERROR_PRIVILEGE_NOT_HELD) || errors.Is(err, windows.ERROR_ACCESS_DENIED) {
				t.Skip("symlink creation permission unavailable", err)
			}
			t.Fatal("directory symlink creation failed", err)
		}
	}
	if err := os.Mkdir(filepath.Join(source, "z-target"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(source, "z-target", "data"), []byte("faithful"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(source, "b-dangling")); err != nil {
		t.Fatal(err)
	}
	if err := createSnapshotSymlink("never-created", filepath.Join(source, "b-dangling"), snapshotDirectoryLink); err != nil {
		t.Fatal(err)
	}
	for _, destination := range []string{filepath.Join(t.TempDir(), "snapshot"), filepath.Join(t.TempDir(), "restored")} {
		original, err := walkSnapshot(context.Background(), source, destination, nil)
		if err != nil {
			t.Fatal(err)
		}
		copied, err := walkSnapshot(context.Background(), destination, "", nil)
		if err != nil || inventoryDigest(original) != inventoryDigest(copied) {
			t.Fatal("link inventory changed", err)
		}
		for _, name := range []string{"a-forward", "b-dangling"} {
			info, err := os.Lstat(filepath.Join(destination, name))
			if err != nil {
				t.Fatal(err)
			}
			kind, err := snapshotSymlinkKind(info)
			if err != nil || kind != snapshotDirectoryLink {
				t.Fatal("directory reparse type lost", name, kind, err)
			}
		}
		raw, err := os.ReadFile(filepath.Join(destination, "a-forward", "data"))
		if err != nil || string(raw) != "faithful" {
			t.Fatal("forward link unusable", err)
		}
		source = destination
	}
}
