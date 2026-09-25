package store

import (
	"context"
	"net/url"
	"path/filepath"
	"testing"
)

func TestDatabaseURIPreservesLocalDriveAndEscapedPaths(t *testing.T) {
	for _, path := range []string{"C:/Users/runner/private #1%/state.sqlite", "d:/database/한국어.sqlite", "/tmp/private #1%/state.sqlite"} {
		for _, readonly := range []bool{false, true} {
			value, err := url.Parse(databaseURI(path, readonly))
			if err != nil {
				t.Fatal(err)
			}
			wantPath := path
			if path[0] != '/' {
				wantPath = "/" + path
			}
			wantMode := "rw"
			if readonly {
				wantMode = "ro"
			}
			if value.Scheme != "file" || value.Host != "" || value.Path != wantPath || value.Fragment != "" || value.Query().Get("mode") != wantMode {
				t.Fatalf("changed database identity or mode: %s", value)
			}
		}
	}
	// On Windows this uses a real native drive path. Every platform also checks
	// that URI escaping opens precisely the intended file and permits backup.
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "private #1% 한국어")
	db, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Backup(ctx); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
}
