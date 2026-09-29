package workspace

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestForkSnapshotRejectsChangesAcrossNativeCreation(t *testing.T) {
	source, target := t.TempDir(), t.TempDir()
	source, _ = filepath.EvalSymlinks(source)
	target, _ = filepath.EvalSymlinks(target)
	path := filepath.Join(source, "file.txt")
	if err := os.Chmod(target, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("before native fork"), 0600); err != nil {
		t.Fatal(err)
	}
	copy, err := copyForkTree(context.Background(), source, target, false)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := &ForkSnapshot{copies: []forkCopy{copy}}
	child := Manifest{State: Ready, Type: domain.GeneralChat, PrimaryPath: target}
	if snapshot.Verify(context.Background(), child) != nil {
		t.Fatal("unchanged copy rejected")
	}
	if err := os.WriteFile(path, []byte("during native fork"), 0600); err != nil {
		t.Fatal(err)
	}
	if snapshot.Verify(context.Background(), child) == nil {
		t.Fatal("source changed during native creation")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := scanForkTree(ctx, source, "", false); err == nil {
		t.Fatal("canceled copy accepted")
	}
}

func TestForkCopyRejectsLinkedDestinationWithoutWritingExternalFiles(t *testing.T) {
	source, external := t.TempDir(), t.TempDir()
	source, _ = filepath.EvalSymlinks(source)
	external, _ = filepath.EvalSymlinks(external)
	if err := os.WriteFile(filepath.Join(source, "source.txt"), []byte("retained source"), 0600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(source, "linked-child")
	if err := os.Symlink(external, target); err != nil {
		t.Skip("native link fixture unavailable")
	}
	if _, err := copyForkTree(context.Background(), source, target, false); err == nil {
		t.Fatal("linked target accepted")
	}
	entries, err := os.ReadDir(external)
	if err != nil || len(entries) != 0 {
		t.Fatal("copy wrote unselected external content", err)
	}
}
