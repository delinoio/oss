package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestPrepareReadOnlyWindowsFile(t *testing.T) {
	root := privateRoot(t)
	src, dst := filepath.Join(root, "source"), filepath.Join(root, "destination")
	if err := os.Mkdir(src, 0700); err != nil {
		t.Fatal(err)
	}
	original := filepath.Join(src, "readonly")
	if err := os.WriteFile(original, []byte("cache data"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(original, 0400); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chmod(original, 0600); os.Chmod(filepath.Join(dst, "readonly"), 0600) })
	if _, err := Prepare(context.Background(), filepath.Join(root, "transaction.json"), []Pair{{Source: src, Destination: dst}}, false, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range []string{original, filepath.Join(dst, "readonly")} {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm()&0200 != 0 {
			t.Fatalf("read-only attribute lost: %s", p)
		}
		data, err := os.ReadFile(p)
		if err != nil || string(data) != "cache data" {
			t.Fatalf("data=%q error=%v", data, err)
		}
	}
}
