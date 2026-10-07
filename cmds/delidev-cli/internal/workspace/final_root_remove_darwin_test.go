// SPDX-License-Identifier: Apache-2.0
//go:build darwin

package workspace

import (
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestVerifyFinalRootAfterUnlinkRejectsMovedOriginal(t *testing.T) {
	parentPath := t.TempDir()
	rootPath := filepath.Join(parentPath, "root")
	movedPath := filepath.Join(parentPath, "moved-root")
	if err := os.Mkdir(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	parent, err := os.Open(parentPath)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	root, err := os.Open(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	identity, err := directoryFileIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	expectedPath, err := filepath.EvalSymlinks(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(rootPath, movedPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(rootPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := unix.Unlinkat(int(parent.Fd()), filepath.Base(rootPath), unix.AT_REMOVEDIR); err != nil {
		t.Fatal(err)
	}
	if err := verifyFinalRootAfterUnlink(root, parent, filepath.Base(rootPath), identity, expectedPath); err == nil {
		t.Fatal("moved original was accepted as unlinked")
	}
}
