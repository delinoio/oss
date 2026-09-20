package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNativeHookCreationRejectsReplacedParent(t *testing.T) {
	s, _ := fixture(t, "version=1\n")
	for _, opened := range []bool{false, true} {
		t.Run(map[bool]string{false: "after-discovery", true: "after-root-open"}[opened], func(t *testing.T) {
			common, external := t.TempDir(), t.TempDir()
			common, err := filepath.EvalSymlinks(common)
			if err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(common, "hooks")
			if err := os.Mkdir(hooks, 0755); err != nil {
				t.Fatal(err)
			}
			if !repositoryHookDirectory(common, hooks) {
				t.Fatal("native directory rejected")
			}
			root, err := os.OpenRoot(common)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			if err := os.Rename(hooks, hooks+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(external, hooks); err != nil {
				t.Skipf("symlinks unavailable: %v", err)
			}
			path := filepath.Join(hooks, "post-commit")
			body := []byte("owned hook")
			record := Installation{ID: "hook:" + path, Path: path, Hash: Hash(body), Kind: "hook"}
			if opened {
				err = s.publishNativeHook(root, "post-commit", record, body)
			} else {
				err = s.createNativeHook(common, "post-commit", record, body)
			}
			if err == nil {
				t.Fatal("redirected parent accepted")
			}
			entries, err := os.ReadDir(external)
			if err != nil || len(entries) != 0 {
				t.Fatal("external directory changed", entries, err)
			}
			if _, err := s.installation(record.ID); err == nil {
				t.Fatal("failed publication gained ownership")
			}
		})
	}
}

func TestHookRollbackCannotFollowReplacedParent(t *testing.T) {
	common, external := t.TempDir(), t.TempDir()
	root, err := os.OpenRoot(common)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = root.Mkdir("hooks", 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join("hooks", "post-commit")
	if err = root.WriteFile(path, []byte("owned"), 0755); err != nil {
		t.Fatal(err)
	}
	info, err := root.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(external, "post-commit"), []byte("external"), 0755); err != nil {
		t.Fatal(err)
	}
	hooks := filepath.Join(common, "hooks")
	if err = os.Rename(hooks, hooks+"-old"); err != nil {
		t.Fatal(err)
	}
	if err = os.Symlink(external, hooks); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if err = rollbackCreatedHook(root, path, info, []byte("owned")); err == nil {
		t.Fatal("redirected rollback accepted")
	}
	data, err := os.ReadFile(filepath.Join(external, "post-commit"))
	if err != nil || string(data) != "external" {
		t.Fatal("external hook changed", err)
	}
}

func TestNativeHookRefreshRejectsRedirectsAndSameByteReplacement(t *testing.T) {
	for _, stage := range []string{"before-snapshot", "after-snapshot", "same-byte-replacement"} {
		t.Run(stage, func(t *testing.T) {
			s, _ := fixture(t, "version=1\n")
			common, external := t.TempDir(), t.TempDir()
			common, err := filepath.EvalSymlinks(common)
			if err != nil {
				t.Fatal(err)
			}
			hooks := filepath.Join(common, "hooks")
			if err := os.Mkdir(hooks, 0755); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(hooks, "post-commit")
			old := []byte("stale owned hook")
			if err := os.WriteFile(path, old, 0755); err != nil {
				t.Fatal(err)
			}
			record := Installation{ID: "hook:" + path, Path: path, Kind: "hook", Hash: Hash(old)}
			if err := s.saveInstallation(record); err != nil {
				t.Fatal(err)
			}
			if !repositoryHookDirectory(common, hooks) {
				t.Fatal("native hooks rejected")
			}
			root, err := os.OpenRoot(common)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			var snapshot agentFile
			if stage != "before-snapshot" {
				snapshot, err = readNativeHook(root, "post-commit")
				if err != nil {
					t.Fatal(err)
				}
			}
			expectedPath := path
			if stage == "same-byte-replacement" {
				if err := AtomicWrite(path, old, 0755); err != nil {
					t.Fatal(err)
				}
			} else {
				expectedPath = filepath.Join(external, "post-commit")
				if err := os.WriteFile(expectedPath, old, 0755); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(hooks, hooks+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(external, hooks); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
				}
			}
			if stage == "before-snapshot" {
				_, err = readNativeHook(root, "post-commit")
			} else {
				_, err = s.refreshHook(root, record, snapshot, []byte("replacement"))
			}
			if err == nil {
				t.Fatal("concurrent hook replacement accepted")
			}
			data, err := os.ReadFile(expectedPath)
			if err != nil || string(data) != string(old) {
				t.Fatal("concurrent hook changed", err)
			}
			current, err := s.installation(record.ID)
			if err != nil || current.Hash != record.Hash || len(current.Original) != 0 {
				t.Fatal("conflict changed ownership", current, err)
			}
			entries, err := os.ReadDir(external)
			if err != nil || stage != "same-byte-replacement" && (len(entries) != 1 || entries[0].Name() != "post-commit") {
				t.Fatal("external staging file created", entries, err)
			}
		})
	}
}
