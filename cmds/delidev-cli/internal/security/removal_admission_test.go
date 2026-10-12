// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func admissionRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return root
}
func captureAdmission(t *testing.T, root string, paths ...string) *RemovalAdmission {
	t.Helper()
	a, err := CaptureRemovalAdmission(context.Background(), root, paths)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	var retained RemovalAdmission
	if err := json.Unmarshal(raw, &retained); err != nil {
		t.Fatal(err)
	}
	return &retained
}
func admissionFile(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("same contents"), 0600); err != nil {
		t.Fatal(err)
	}
}
func TestOwnedTreeAdmissionRejectsReplacementAndRetainsOriginal(t *testing.T) {
	for _, kind := range []string{"file", "tree", "ancestor"} {
		t.Run(kind, func(t *testing.T) {
			root := admissionRoot(t)
			path := filepath.Join(root, "managed", "copy")
			sentinel := path
			if kind != "file" {
				sentinel = filepath.Join(path, "sentinel")
			}
			admissionFile(t, sentinel)
			a := captureAdmission(t, root, path)
			exchanged := path
			if kind == "ancestor" {
				exchanged = filepath.Dir(path)
			}
			old := exchanged + "-original"
			if err := os.Rename(exchanged, old); err != nil {
				t.Fatal(err)
			}
			admissionFile(t, sentinel)
			if err := RemoveAdmittedTree(context.Background(), root, path, a); err == nil {
				t.Fatal("replacement admitted")
			}
			if raw, err := os.ReadFile(sentinel); err != nil || string(raw) != "same contents" {
				t.Fatal("foreign sentinel removed", err)
			}
			if err := os.Rename(exchanged, exchanged+"-foreign"); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(old, exchanged); err != nil {
				t.Fatal(err)
			}
			if err := RemoveAdmittedTree(context.Background(), root, path, a); err != nil {
				t.Fatal("unchanged original rejected", err)
			}
			if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("original survived", err)
			}
		})
	}
}
func TestOwnedTreeAdmissionExpectedAbsenceNeverUnlinksOrSyncs(t *testing.T) {
	for _, parentPresent := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing namespace", true: "present namespace"}[parentPresent], func(t *testing.T) {
			root := admissionRoot(t)
			path := filepath.Join(root, "managed", "absent")
			if parentPresent {
				if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
					t.Fatal(err)
				}
			}
			a := captureAdmission(t, root, path)
			unlinks, syncs := 0, 0
			remove := func(string) error { unlinks++; return nil }
			sync := func(string) error { syncs++; return nil }
			if err := removeAdmittedTree(context.Background(), root, path, a, nil, remove, sync); err != nil {
				t.Fatal(err)
			}
			if unlinks != 0 || syncs != 0 {
				t.Fatal("absence invented removal work")
			}
			admissionFile(t, path)
			if err := removeAdmittedTree(context.Background(), root, path, a, nil, remove, sync); err == nil {
				t.Fatal("later creation admitted")
			}
			if unlinks != 0 || syncs != 0 {
				t.Fatal("later creation touched")
			}
		})
	}
}
func TestOwnedTreeAdmissionDisappearedOriginalSyncsOriginalParent(t *testing.T) {
	root := admissionRoot(t)
	path := filepath.Join(root, "managed", "file")
	admissionFile(t, path)
	a := captureAdmission(t, root, path)
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	syncs := 0
	sync := func(p string) error {
		if p != path {
			t.Fatal("foreign sync operand")
		}
		syncs++
		return nil
	}
	if err := removeAdmittedTree(context.Background(), root, path, a, nil, os.Remove, sync); err != nil || syncs != 1 {
		t.Fatal("original disappearance not synchronized", err, syncs)
	}
	if err := os.Rename(filepath.Dir(path), filepath.Dir(path)+"-old"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := removeAdmittedTree(context.Background(), root, path, a, nil, os.Remove, sync); err == nil || syncs != 1 {
		t.Fatal("foreign parent synchronized", err, syncs)
	}
}
func TestOwnedTreeAdmissionRechecksEachDestructiveBoundary(t *testing.T) {
	root := admissionRoot(t)
	path := filepath.Join(root, "managed", "file")
	admissionFile(t, path)
	a := captureAdmission(t, root, path)
	hook := func(p string) {
		if p != path {
			t.Fatal("unexpected boundary")
		}
		if err := os.Rename(path, path+"-original"); err != nil {
			t.Fatal(err)
		}
		admissionFile(t, path)
	}
	if err := removeAdmittedTree(context.Background(), root, path, a, hook, os.Remove, SyncParent); err == nil {
		t.Fatal("boundary replacement admitted")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("replacement disappeared", err)
	}
}
func TestOwnedTreeAdmissionRejectsUnknownNamesAndResumesOriginal(t *testing.T) {
	root := admissionRoot(t)
	path := filepath.Join(root, "managed", "copy")
	admissionFile(t, filepath.Join(path, "a"))
	admissionFile(t, filepath.Join(path, "b"))
	a := captureAdmission(t, root, path)
	admissionFile(t, filepath.Join(path, "foreign"))
	if err := RemoveAdmittedTree(context.Background(), root, path, a); err == nil {
		t.Fatal("new child admitted")
	}
	if err := os.Rename(filepath.Join(path, "foreign"), filepath.Join(root, "foreign")); err != nil {
		t.Fatal(err)
	}
	count := 0
	interrupted := errors.New("interrupted unlink")
	remove := func(p string) error {
		count++
		if count == 2 {
			return interrupted
		}
		return os.Remove(p)
	}
	if err := removeAdmittedTree(context.Background(), root, path, a, nil, remove, SyncParent); err == nil {
		t.Fatal("interruption ignored")
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, a); err != nil {
		t.Fatal("original resume failed", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "foreign")); err != nil {
		t.Fatal("unrelated child changed", err)
	}
}
func TestOwnedTreeAdmissionCancellationAndMalformedMetadata(t *testing.T) {
	root := admissionRoot(t)
	path := filepath.Join(root, "managed", "file")
	admissionFile(t, path)
	a := captureAdmission(t, root, path)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := CaptureRemovalAdmission(ctx, root, []string{path}); err == nil {
		t.Fatal("capture ignored cancellation")
	}
	if err := RemoveAdmittedTree(ctx, root, path, a); err == nil {
		t.Fatal("unlink ignored cancellation")
	}
	a.Entries = append(a.Entries, RemovalEntry{Path: "../foreign"})
	if a.Validate() == nil {
		t.Fatal("unconfined metadata accepted")
	}
	if _, err := os.Lstat(path); err != nil {
		t.Fatal("cancellation changed original", err)
	}
}
func TestOwnedTreeAdmissionDoesNotFollowOriginalSymlink(t *testing.T) {
	root := admissionRoot(t)
	original := filepath.Join(root, "unrelated")
	admissionFile(t, original)
	link := filepath.Join(root, "link")
	if err := os.Symlink(original, link); err != nil {
		t.Skip("platform does not permit symlink fixture")
	}
	a := captureAdmission(t, root, link)
	if err := RemoveAdmittedTree(context.Background(), root, link, a); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(original); err != nil {
		t.Fatal("symlink target changed", err)
	}
}

func TestRemovalNamespaceAdmissionRejectsSwapWhileExtendingInventory(t *testing.T) {
	ctx := context.Background()
	root := admissionRoot(t)
	namespace := filepath.Join(root, "title-runtimes")
	path := filepath.Join(namespace, "copy")
	admissionFile(t, path)
	pin, err := CaptureRemovalNamespaces(ctx, root, []string{namespace})
	if err != nil {
		t.Fatal(err)
	}
	// Enumeration has selected an operand; replacing its namespace before
	// native operand capture must not turn a foreign copy into the baseline.
	operands := []string{path}
	if err := os.Rename(namespace, namespace+"-original"); err != nil {
		t.Fatal(err)
	}
	admissionFile(t, path)
	if _, err := pin.Extend(ctx, root, operands); err == nil {
		t.Fatal("foreign namespace acquired inventory authority")
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != "same contents" {
		t.Fatal("foreign contents changed", err)
	}
}
