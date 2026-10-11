// SPDX-License-Identifier: Apache-2.0
//go:build windows

package workspace

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirectoryIdentityAcceptsGitSeparatorsAndRejectsReplacement(t *testing.T) {
	parent, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "owned")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	native, err := directoryPathIdentity(root)
	if err != nil {
		t.Fatal(err)
	}
	gitPath := filepath.ToSlash(root)
	fromGit, err := directoryPathIdentity(gitPath)
	if err != nil || native != fromGit {
		t.Fatalf("Git path changed directory identity: %v", err)
	}
	if _, err := directoryPathIdentity(gitPath + "/."); err == nil {
		t.Fatal("noncanonical path acquired ownership")
	}
	if err := os.Rename(root, root+"-original"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	replacement, err := directoryPathIdentity(gitPath)
	if err != nil || replacement == native {
		t.Fatalf("replacement retained original identity: %v", err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(root, alias); err != nil {
		t.Skipf("directory symlink unavailable: %v", err)
	}
	if _, err := directoryPathIdentity(filepath.ToSlash(alias)); err == nil {
		t.Fatal("directory alias acquired ownership")
	}
}
