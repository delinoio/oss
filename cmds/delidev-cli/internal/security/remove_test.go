// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func removeFixtureRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	// Managed paths always live below a directory inside the root.
	managed := filepath.Join(root, "managed")
	if err := os.Mkdir(managed, 0700); err != nil {
		t.Fatal(err)
	}
	return root
}

func writeFixture(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
}

func requirePending(t *testing.T, err error) {
	t.Helper()
	if err == nil || domain.SafeError(err).Code != domain.RecoveryRequired {
		t.Fatal("replacement was not reported as pending cleanup", err)
	}
}

func TestRemoveOwnedTreeIdentityRemovesUnchangedDirectoryAndFile(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	file := filepath.Join(root, "managed", "file.json")
	writeFixture(t, filepath.Join(dir, "a", "b"), "owned")
	writeFixture(t, file, "owned")
	for _, path := range []string{dir, file} {
		identity, err := CapturePathIdentity(path)
		if err != nil || !identity.Present() {
			t.Fatal(identity, err)
		}
		if err := RemoveOwnedTreeIdentity(context.Background(), root, path, identity); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Lstat(path); !os.IsNotExist(err) {
			t.Fatal("unchanged original survived", err)
		}
	}
}

func TestRemoveOwnedTreeIdentityRejectsReplacements(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	writeFixture(t, filepath.Join(dir, "owned"), "original")
	identity, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer identity.Close()
	if err := os.Rename(dir, filepath.Join(root, "managed", "moved")); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "owned"), "original")
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, dir, identity))
	if _, err := os.Lstat(filepath.Join(dir, "owned")); err != nil {
		t.Fatal("same-content replacement directory was removed", err)
	}

	file := filepath.Join(root, "managed", "file.json")
	writeFixture(t, file, "same")
	identity, err = CapturePathIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(file, file+".moved"); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, file, "same")
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, file, identity))
	if _, err := os.Lstat(file); err != nil {
		t.Fatal("same-content replacement file was removed", err)
	}
}

func TestRemoveOwnedTreeIdentityRejectsRecreatedFileWithReusedInode(t *testing.T) {
	root := removeFixtureRoot(t)
	file := filepath.Join(root, "managed", "file.json")
	writeFixture(t, file, "same")
	identity, err := CapturePathIdentity(file)
	if err != nil {
		t.Fatal(err)
	}
	// Rewriting in place keeps dev/inode but changes the modification time.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(file, later, later); err != nil {
		t.Fatal(err)
	}
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, file, identity))
	if _, err := os.Lstat(file); err != nil {
		t.Fatal("modified object was removed", err)
	}
}

func TestRemoveOwnedTreeIdentityKeepsProvenAbsenceAndInvalidatesOnCreation(t *testing.T) {
	root := removeFixtureRoot(t)
	path := filepath.Join(root, "managed", "absent")
	identity, err := CapturePathIdentity(path)
	if err != nil || identity.Present() {
		t.Fatal(identity, err)
	}
	if err := RemoveOwnedTreeIdentity(context.Background(), root, path, identity); err != nil {
		t.Fatal("proven absence blocked", err)
	}
	writeFixture(t, filepath.Join(path, "sentinel"), "foreign")
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, path, identity))
	if _, err := os.Lstat(filepath.Join(path, "sentinel")); err != nil {
		t.Fatal("new creation was removed", err)
	}
}

func TestRemoveOwnedTreeIdentityAcceptsOriginalAlreadyRemoved(t *testing.T) {
	root := removeFixtureRoot(t)
	path := filepath.Join(root, "managed", "gone")
	writeFixture(t, path, "owned")
	identity, err := CapturePathIdentity(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveOwnedTreeIdentity(context.Background(), root, path, identity); err != nil {
		t.Fatal(err)
	}
}

func TestPathIdentityHoldsObjectSoReplacementCannotReuseItsInode(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	identity, err := CapturePathIdentity(dir)
	if err != nil || !identity.Held() {
		t.Fatal("capture did not hold the object", identity, err)
	}
	defer identity.Close()
	// Delete the validated directory and recreate the path. While the handle is
	// open the original inode stays allocated, so the replacement is a different
	// object even though a bare device/inode comparison would otherwise be free
	// to see a reused number.
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	writeFixture(t, filepath.Join(dir, "sentinel"), "foreign")
	replacement, err := CapturePathIdentity(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.Close()
	if replacement.Record().Inode == identity.Record().Inode {
		t.Fatal("replacement reused the inode of a held object")
	}
	requirePending(t, RemoveOwnedTreeIdentity(context.Background(), root, dir, identity))
	if _, err := os.Lstat(filepath.Join(dir, "sentinel")); err != nil {
		t.Fatal("replacement directory was removed", err)
	}
}

func TestPathIdentityRecordRebindsOnlyToTheSameObject(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	file := filepath.Join(root, "managed", "file.json")
	writeFixture(t, filepath.Join(dir, "owned"), "owned")
	writeFixture(t, file, "owned")
	for _, path := range []string{dir, file} {
		captured, err := CapturePathIdentity(path)
		if err != nil {
			t.Fatal(err)
		}
		record := captured.Record()
		captured.Close()
		// Restoring the record binds the same object.
		restored, err := ReopenPathIdentity(path, record)
		if err != nil || !restored.Held() {
			t.Fatal("record did not rebind to its object", err)
		}
		restored.Close()
		// A same-content replacement at the path never rebinds.
		if err := os.Rename(path, path+".moved"); err != nil {
			t.Fatal(err)
		}
		if path == dir {
			writeFixture(t, filepath.Join(dir, "owned"), "owned")
		} else {
			writeFixture(t, file, "owned")
		}
		if _, err := ReopenPathIdentity(path, record); err == nil {
			t.Fatal("record rebound to a replacement")
		}
		// A vanished original counts as removed.
		if err := os.RemoveAll(path); err != nil {
			t.Fatal(err)
		}
		vanished, err := ReopenPathIdentity(path, record)
		if err != nil || vanished.Held() {
			t.Fatal("vanished original was not accepted", err)
		}
		if err := RemoveOwnedTreeIdentity(context.Background(), root, path, vanished); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRemoveOwnedTreeRebindsToTheValidatedObjectOnly(t *testing.T) {
	root := removeFixtureRoot(t)
	dir := filepath.Join(root, "managed", "dir")
	writeFixture(t, filepath.Join(dir, "owned"), "owned")
	if err := RemoveOwnedTree(context.Background(), root, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(dir); !os.IsNotExist(err) {
		t.Fatal("baseline removal left the directory", err)
	}
}
