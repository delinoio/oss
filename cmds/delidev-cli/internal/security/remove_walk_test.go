// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The path-based remover is the production Windows implementation. It is
// portable Go, so these tests exercise its algorithm on every host.

func walkFixture(t *testing.T) (root, dir string) {
	t.Helper()
	root = removeFixtureRoot(t)
	dir = filepath.Join(root, "managed", "dir")
	writeFixture(t, filepath.Join(dir, "top.json"), "owned")
	writeFixture(t, filepath.Join(dir, "nested", "deep", "leaf.json"), "owned")
	writeFixture(t, filepath.Join(dir, "nested", "mid.json"), "owned")
	return root, dir
}

func TestRemoveTreeByPathRemovesUnchangedTreeWithHeldRoot(t *testing.T) {
	root, dir := walkFixture(t)
	identity, err := CapturePathIdentity(dir)
	if err != nil || !identity.Held() {
		t.Fatal(identity, err)
	}
	defer identity.Close()
	// The captured root stays open throughout: removal must compare handle-derived
	// records, never path-based FileInfo that conflicts with a held handle.
	if err := removeTreeByPath(context.Background(), dir, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("tree survived", err)
	}
	_ = root
}

func TestRemoveTreeByPathRejectsNestedDirectoryReplacedBeforeListing(t *testing.T) {
	root, dir := walkFixture(t)
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	nested := filepath.Join(dir, "nested")
	walkAfterOpenDirHook = func(path string) {
		if path != nested {
			return
		}
		if err := os.Rename(nested, filepath.Join(root, "managed", "moved")); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(nested, "foreign", "sentinel"), "foreign")
	}
	t.Cleanup(func() { walkAfterOpenDirHook = nil })
	requirePending(t, removeTreeByPath(context.Background(), dir, identity))
	if raw, err := os.ReadFile(filepath.Join(nested, "foreign", "sentinel")); err != nil || string(raw) != "foreign" {
		t.Fatal("replacement directory's descendants were removed", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "top.json")); err != nil {
		t.Fatal("removal started before the inventory was trustworthy", err)
	}
}

func TestRemoveTreeByPathRejectsAncestorReplacedAfterInventory(t *testing.T) {
	root, dir := walkFixture(t)
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	nested := filepath.Join(dir, "nested")
	replacementBlocked := false
	walkBeforeUnlinkHook = func() {
		if err := os.Rename(nested, filepath.Join(root, "managed", "moved")); err != nil {
			// Windows refuses renaming an ancestor while its descendants are held.
			// The OS then prevents the replacement before our identity check.
			if runtime.GOOS == "windows" && os.IsPermission(err) {
				replacementBlocked = true
				return
			}
			t.Fatal(err)
		}
		// Same names as the inventoried descendants, so only the ancestor check
		// can tell them apart from the originals.
		writeFixture(t, filepath.Join(nested, "deep", "leaf.json"), "owned")
		writeFixture(t, filepath.Join(nested, "mid.json"), "owned")
	}
	t.Cleanup(func() { walkBeforeUnlinkHook = nil })
	err = removeTreeByPath(context.Background(), dir, identity)
	if replacementBlocked {
		if err != nil {
			t.Fatal("OS-blocked replacement prevented original cleanup", err)
		}
		if _, err := os.Lstat(dir); !os.IsNotExist(err) {
			t.Fatal("original tree survived cleanup", err)
		}
		return
	}
	requirePending(t, err)
	for _, relative := range []string{filepath.Join("deep", "leaf.json"), "mid.json"} {
		if _, err := os.Lstat(filepath.Join(nested, relative)); err != nil {
			t.Fatal("descendant of a replaced ancestor was removed", relative, err)
		}
	}
}

func TestRemoveTreeByPathDoesNotFollowSymlinks(t *testing.T) {
	root, dir := walkFixture(t)
	outside := filepath.Join(root, "managed", "outside")
	writeFixture(t, filepath.Join(outside, "keep"), "keep")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	if err := removeTreeByPath(context.Background(), dir, identity); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || !strings.Contains(string(raw), "keep") {
		t.Fatal("symlink target removed", err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("tree survived", err)
	}
}
