package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestInitCannotEscapeWorktreeThroughConfigSymlink(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "missing-external-file"
		if existing {
			name = "existing-external-file"
		}
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			repo, outside := filepath.Join(base, "repo"), filepath.Join(base, "outside")
			for _, path := range []string{repo, outside} {
				if err := os.Mkdir(path, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.Symlink(outside, filepath.Join(repo, ".config")); err != nil {
				t.Skipf("host does not permit symlink fixture: %v", err)
			}
			file := filepath.Join(outside, filepath.Base(ProjectFile))
			if existing {
				if err := os.WriteFile(file, []byte("unrelated configuration"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			s, err := Open(Paths{Config: filepath.Join(base, "config.toml"), State: filepath.Join(base, "state"), Control: filepath.Join(base, "control")})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if _, err := Git(context.Background(), repo, "init", "--quiet", "--template="); err != nil {
				t.Fatal(err)
			}
			if _, _, err := s.Init(context.Background(), repo); err == nil {
				t.Fatal("escaped configuration path accepted")
			}
			entries, err := os.ReadDir(outside)
			if err != nil || (!existing && len(entries) != 0) || (existing && len(entries) != 1) {
				t.Fatal("external directory changed", entries, err)
			}
			if existing {
				if data, err := os.ReadFile(file); err != nil || string(data) != "unrelated configuration" {
					t.Fatal("external file changed", err)
				}
			}
			var count int
			if err := s.Store.DB.QueryRow("SELECT count(*) FROM repositories").Scan(&count); err != nil || count != 0 {
				t.Fatal("unsafe initialization registered trust", count, err)
			}
		})
	}
}

func TestOwnedConfigurationCreationIsAtomicAndPreservesExistingFiles(t *testing.T) {
	root, err := os.OpenRoot(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	results := make(chan error, 8)
	for range 8 {
		go func() { results <- AtomicCreateOwned(root, ProjectFile, []byte(DefaultProject), 0600) }()
	}
	created := 0
	for range 8 {
		if err := <-results; err == nil {
			created++
		} else if !os.IsExist(err) {
			t.Fatal(err)
		}
	}
	if created != 1 {
		t.Fatalf("created %d configurations", created)
	}
	if err := AtomicCreateOwned(root, ProjectFile, []byte("replacement"), 0600); !os.IsExist(err) {
		t.Fatal("existing configuration overwritten", err)
	}
	if data, err := root.ReadFile(ProjectFile); err != nil || string(data) != DefaultProject {
		t.Fatal("configuration contents changed", err)
	}
	parent, err := root.Open(".config")
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	entries, err := parent.ReadDir(-1)
	if err != nil || len(entries) != 1 {
		t.Fatal("temporary staging files retained", entries, err)
	}
}
