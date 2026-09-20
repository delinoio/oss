package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStateDirectoryCreatesPrivateLeafWithTrailingSeparator(t *testing.T) {
	root := filepath.Join(t.TempDir(), "parent", "state") + string(os.PathSeparator)
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	private, err := stateDirectoryPrivate(filepath.Clean(root))
	if err != nil || !private {
		t.Fatal("new leaf is not private", err)
	}
	reopened, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}
