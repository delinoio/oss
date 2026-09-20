package core

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestStatePathsPreserveURICharactersAndDurableSettings(t *testing.T) {
	names := []string{"space 한국어 #fragment %23 &mode=memory", "literal%3Fquery"}
	if runtime.GOOS != "windows" {
		names = append(names, "literal?mode=memory#fragment")
	}
	// APFS and Windows reject arbitrary invalid UTF-8 filename bytes.
	if runtime.GOOS == "linux" {
		names = append(names, "raw-\xff?query")
	}
	for _, name := range names {
		t.Run(name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, name)
			store, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err = store.DB.Exec("INSERT INTO pairings(code_hash,expires) VALUES('retained','later')"); err != nil {
				t.Fatal(err)
			}
			if info, err := os.Stat(filepath.Join(root, "state.sqlite")); err != nil || !info.Mode().IsRegular() {
				t.Fatal("wrong database location", err)
			}
			var journal string
			var foreign, busy, sync int
			for _, probe := range []struct {
				query string
				value any
			}{{"PRAGMA journal_mode", &journal}, {"PRAGMA foreign_keys", &foreign}, {"PRAGMA busy_timeout", &busy}, {"PRAGMA synchronous", &sync}} {
				if err = store.DB.QueryRow(probe.query).Scan(probe.value); err != nil {
					t.Fatal(err)
				}
			}
			if journal != "wal" || foreign != 1 || busy != 15000 || sync != 2 {
				t.Fatalf("connection settings changed: %s %d %d %d", journal, foreign, busy, sync)
			}
			if err = store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := OpenStore(root)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			var expires string
			if err = reopened.DB.QueryRow("SELECT expires FROM pairings WHERE code_hash='retained'").Scan(&expires); err != nil || expires != "later" {
				t.Fatal("state was not retained", err)
			}
			entries, err := os.ReadDir(parent)
			if err != nil || len(entries) != 1 || entries[0].Name() != name {
				t.Fatal("unexpected redirected database", entries, err)
			}
		})
	}
}
