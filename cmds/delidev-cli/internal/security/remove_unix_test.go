//go:build !windows

// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A directory swapped in for the root after it was opened and validated must
// never lose its contents: traversal and unlinking stay anchored to the opened
// original, and the final name no longer resolves to it.
func TestRemoveOwnedTreeIdentityNeverEntersRootSwappedMidRemoval(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	writeFixture(t, filepath.Join(dir, "owned", "nested"), "original")
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	moved := filepath.Join(root, "managed", "moved")
	removeTreeAfterOpenHook = func() {
		if err := os.Rename(dir, moved); err != nil {
			t.Fatal(err)
		}
		writeFixture(t, filepath.Join(dir, "owned", "nested"), "foreign")
		writeFixture(t, filepath.Join(dir, "sibling"), "foreign")
	}
	t.Cleanup(func() { removeTreeAfterOpenHook = nil })
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, dir, identity))
	for _, relative := range []string{filepath.Join("owned", "nested"), "sibling"} {
		if raw, err := os.ReadFile(filepath.Join(dir, relative)); err != nil || string(raw) != "foreign" {
			t.Fatal("replacement root lost a descendant", relative, err)
		}
	}
}

// A symlink inside the validated tree is unlinked itself, never followed.
func TestRemoveOwnedTreeIdentityDoesNotFollowSymlinks(t *testing.T) {
	root := removeFixtureRoot(t)
	outside := filepath.Join(root, "managed", "outside")
	writeFixture(t, filepath.Join(outside, "keep"), "keep")
	dir := filepath.Join(root, "managed", "dir")
	writeFixture(t, filepath.Join(dir, "owned"), "owned")
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	if err := RemoveOwnedTreeIdentity(context.Background(), root, dir, identity); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("tree survived", err)
	}
	if raw, err := os.ReadFile(filepath.Join(outside, "keep")); err != nil || string(raw) != "keep" {
		t.Fatal("symlink target was removed", err)
	}
}
