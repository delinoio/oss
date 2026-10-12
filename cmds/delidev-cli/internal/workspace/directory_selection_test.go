// SPDX-License-Identifier: Apache-2.0
package workspace

import (
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkingDirectorySelectionOriginalRootAndIdentity(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	selection, err := selectDirectory(root, "src/nested")
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Close()
	if selection.Path() != filepath.Join(root, "src", "nested") || selection.Verify() != nil {
		t.Fatal("nested original directory not retained")
	}
	if err := os.Rename(filepath.Join(root, "src"), filepath.Join(root, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src", "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	if selection.Verify() == nil {
		t.Fatal("same-name replacement adopted")
	}
}
func TestWorkingDirectorySelectionRejectsEscapesAndLinks(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	outside, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escaped")); err != nil {
		t.Skip("symlinks unavailable", err)
	}
	if err := os.Symlink(filepath.Join(root, "nested"), filepath.Join(root, "internal")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "file"), []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, relative := range []string{"../outside", outside, "nested/../nested", "nested//child", "nested\\child", "escaped", "internal", "missing", "file"} {
		t.Run(relative, func(t *testing.T) {
			selection, err := selectDirectory(root, relative)
			if selection != nil {
				_ = selection.Close()
			}
			if err == nil {
				t.Fatal("unsupported destination admitted")
			}
		})
	}
}
func TestWorkingDirectorySelectionUsesManifestRepositoryAndClosedOwner(t *testing.T) {
	primary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	first, second := domain.NewID(), domain.NewID()
	if err := os.Mkdir(filepath.Join(secondary, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	inspection := &ClosedExecutionInspection{release: func() error { return nil }, cwd: primary, manifest: Manifest{PrimaryPath: primary, Repositories: []PreparedRepository{{ID: first, Path: primary}, {ID: second, Path: secondary}}}}
	if selection, err := inspection.SelectDirectory(domain.NewID(), ""); err == nil {
		selection.Close()
		t.Fatal("foreign repository authorized")
	}
	selection, err := inspection.SelectDirectory(second, "nested")
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Close()
	if selection.Path() != filepath.Join(secondary, "nested") || inspection.WorkingDirectory() != primary {
		t.Fatal("directory selection relocated preparation")
	}
	if err := inspection.Close(); err != nil {
		t.Fatal(err)
	}
	if selection.Verify() == nil {
		t.Fatal("released original owner retained authority")
	}
	if selection, err := inspection.SelectDirectory(first, ""); err == nil {
		selection.Close()
		t.Fatal("closed owner authorized another selection")
	}
}

func TestWorkingDirectorySelectionExecutionLeasePreservesPrimary(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "nested"), 0700); err != nil {
		t.Fatal(err)
	}
	lease := &ExecutionLease{cwd: root, manifest: Manifest{PrimaryPath: root}}
	selection, err := lease.SelectDirectory("", "nested")
	if err != nil {
		t.Fatal(err)
	}
	defer selection.Close()
	if lease.WorkingDirectory() != root || selection.Path() != filepath.Join(root, "nested") {
		t.Fatal("selected directory rewrote source root")
	}
	lease.closed.Store(true)
	if selection.Verify() == nil {
		t.Fatal("closed lease preserved selection authority")
	}
	if other, err := lease.SelectDirectory("", "nested"); err == nil {
		_ = other.Close()
		t.Fatal("closed lease admitted new selection")
	}
}
