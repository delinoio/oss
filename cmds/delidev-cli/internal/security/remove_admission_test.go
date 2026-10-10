// SPDX-License-Identifier: Apache-2.0
package security

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestOwnedTreeAdmissionRejectsReplacementsAndPreservesAbsence(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "copies")
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(parent, "owned")
	admitted, err := CaptureOwnedTree(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, admitted); err != nil {
		t.Fatal("proven absence failed", err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sentinel"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, admitted); err == nil {
		t.Fatal("new creation adopted")
	}
	original, err := CaptureOwnedTree(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, path+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "sentinel"), []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, original); err == nil {
		t.Fatal("same-content replacement adopted")
	}
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+"-original", path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, original); err != nil {
		t.Fatal("original did not delete", err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatal(err)
	}
}

func TestOwnedTreeAdmissionRejectsReplacedAncestorWithMovedOriginalRoot(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := filepath.Join(root, "copies")
	path := filepath.Join(parent, "owned")
	if err := os.MkdirAll(path, 0700); err != nil {
		t.Fatal(err)
	}
	sentinel := filepath.Join(path, "sentinel")
	if err := os.WriteFile(sentinel, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	original, err := CaptureOwnedTree(root, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(parent, parent+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(parent, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(parent+"-original", "owned"), path); err != nil {
		t.Fatal(err)
	}
	if err := RemoveAdmittedTree(context.Background(), root, path, original); err == nil {
		t.Fatal("replacement ancestor borrowed original root")
	}
	if data, err := os.ReadFile(sentinel); err != nil || string(data) != "untouched" {
		t.Fatal("original root removed through foreign ancestor", err)
	}
}
