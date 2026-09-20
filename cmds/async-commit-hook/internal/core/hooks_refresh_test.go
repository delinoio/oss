package core

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookReinstallationRefreshesOwnedConfiguration(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	ctx := context.Background()
	oldBodies := map[string][]byte{}
	for _, kind := range []string{"post-commit", "pre-push"} {
		path, err := hookPath(ctx, repo, kind)
		if err != nil {
			t.Fatal(err)
		}
		// Represent an installation made by a binary that has since moved.
		body := hookBody(filepath.Join(t.TempDir(), "previous ach"), s.Paths.Config, kind)
		if err = AtomicWrite(path, body, 0755); err != nil {
			t.Fatal(err)
		}
		if err = s.saveInstallation(Installation{ID: "hook:" + path, Path: path, Hash: Hash(body), Kind: "hook"}); err != nil {
			t.Fatal(err)
		}
		oldBodies[path] = body
	}
	s.Paths.Config = filepath.Join(filepath.Dir(s.Paths.Config), "new ' configuration.toml")
	result, err := s.Hook(ctx, repo, true, false)
	if err != nil || len(result) != 2 {
		t.Fatalf("refresh: %+v %v", result, err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range result {
		want := hookBody(executable, s.Paths.Config, filepath.Base(item.Path))
		got, err := os.ReadFile(item.Path)
		if err != nil || !item.Installed || !bytes.Equal(got, want) {
			t.Fatalf("stale hook: %+v %q %v", item, got, err)
		}
		backup, err := os.ReadFile(item.Backup)
		if err != nil || !bytes.Equal(backup, oldBodies[item.Path]) {
			t.Fatalf("missing original hook backup: %v", err)
		}
		owned, err := s.installation("hook:" + item.Path)
		if err != nil || owned.Hash != Hash(want) || len(owned.Original) != 0 {
			t.Fatalf("ownership not updated: %+v %v", owned, err)
		}
	}
	result, err = s.Hook(ctx, repo, true, false)
	if err != nil || len(result) != 2 || result[0].Backup != "" || result[1].Backup != "" {
		t.Fatalf("unchanged reinstall not idempotent: %+v %v", result, err)
	}
	if _, err = s.Hook(ctx, repo, true, true); err != nil {
		t.Fatal(err)
	}
}

func TestHookRefreshRecoversOwnershipFailures(t *testing.T) {
	for _, phase := range []string{"intent", "completion", "before-publication"} {
		t.Run(phase, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n")
			ctx := context.Background()
			items, err := s.Hook(ctx, repo, false, false)
			if err != nil {
				t.Fatal(err)
			}
			path := items[0].Path
			old, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			s.Paths.Config += ".new"
			executable, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			want := hookBody(executable, s.Paths.Config, "post-commit")
			if phase == "before-publication" {
				if err = s.saveInstallation(Installation{ID: "hook:" + path, Path: path, Kind: "hook", Hash: Hash(want), Original: old}); err != nil {
					t.Fatal(err)
				}
			} else {
				condition := "IS NOT NULL"
				if phase == "completion" {
					condition = "IS NULL"
				}
				if _, err = s.Store.DB.Exec("CREATE TRIGGER reject_refresh BEFORE UPDATE ON installations WHEN json_extract(NEW.record, '$.original') " + condition + " BEGIN SELECT RAISE(ABORT, 'injected refresh failure'); END"); err != nil {
					t.Fatal(err)
				}
				if _, err = s.Hook(ctx, repo, false, false); err == nil || !strings.Contains(err.Error(), "injected refresh failure") {
					t.Fatalf("missing persistence failure: %v", err)
				}
				got, err := os.ReadFile(path)
				expected := old
				if phase == "completion" {
					expected = want
				}
				if err != nil || !bytes.Equal(got, expected) {
					t.Fatalf("incomplete hook publication: %q %v", got, err)
				}
				if _, err = s.Store.DB.Exec("DROP TRIGGER reject_refresh"); err != nil {
					t.Fatal(err)
				}
			}
			items, err = s.Hook(ctx, repo, false, false)
			if err != nil || !items[0].Installed {
				t.Fatalf("retry failed: %+v %v", items, err)
			}
			got, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("retry did not refresh: %q %v", got, err)
			}
			if _, err = s.Hook(ctx, repo, false, true); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestHookRefreshPreservesUserEditsAndReplacements(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	ctx := context.Background()
	items, err := s.Hook(ctx, repo, false, false)
	if err != nil {
		t.Fatal(err)
	}
	path := items[0].Path
	old, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	user := []byte("#!/bin/sh\necho user edit\n")
	if err = AtomicWrite(path, user, 0755); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(filepath.Dir(path)))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	previous := agentFile{path: filepath.Join("hooks", filepath.Base(path)), info: info, data: old}
	if err = replaceOwnedHook(root, previous, []byte("replacement")); err == nil {
		t.Fatal("replaced a concurrently changed hook")
	}
	s.Paths.Config += ".new"
	items, err = s.Hook(ctx, repo, false, false)
	if err != nil || items[0].Installed || items[0].Manual == "" {
		t.Fatalf("edited hook should require manual integration: %+v %v", items, err)
	}
	got, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(got, user) {
		t.Fatalf("user hook changed: %q %v", got, err)
	}
}
