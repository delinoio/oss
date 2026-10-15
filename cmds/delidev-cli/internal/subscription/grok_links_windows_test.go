package subscription

import (
	"os"
	"path/filepath"
	"testing"
)

// publishGrokFile renames through the held directory and flushes the renamed
// file; the published credential reads back and leaves no temporary behind.
func TestPublishGrokFileFlushesTheRenamedEntry(t *testing.T) {
	home := grokHomeForTest(t)
	bound := boundGrokHome(t, home)
	file, err := bound.root.OpenFile("temporary", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if _, err := file.Write([]byte("published")); err != nil {
		t.Fatal(err)
	}
	if err := publishGrokFile(bound.root, file, "temporary", GrokAuthFileName); err != nil {
		t.Fatalf("publication failed: %v", err)
	}
	if raw, err := os.ReadFile(filepath.Join(home, GrokAuthFileName)); err != nil || string(raw) != "published" {
		t.Fatal("the published entry did not read back")
	}
	if _, err := os.Lstat(filepath.Join(home, "temporary")); !os.IsNotExist(err) {
		t.Fatal("the temporary name survived the publication")
	}
}
