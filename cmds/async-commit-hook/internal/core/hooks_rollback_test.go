package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHookOwnershipFailureRollsBackNewHookAndAllowsRetry(t *testing.T) {
	for _, kind := range []string{"post-commit", "pre-push"} {
		t.Run(kind, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n")
			ctx := context.Background()
			_, err := s.Store.DB.Exec("CREATE TRIGGER reject_hook BEFORE INSERT ON installations WHEN NEW.id LIKE '%/" + kind + "' OR NEW.id LIKE '%\\" + kind + "' BEGIN SELECT RAISE(ABORT, 'injected ownership failure'); END")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.Hook(ctx, repo, true, false); err == nil || !strings.Contains(err.Error(), "injected ownership failure") {
				t.Fatalf("ownership failure not propagated: %v", err)
			}
			path, err := hookPath(ctx, repo, kind)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("unowned hook survived: %v", err)
			}
			if _, err = s.Store.DB.Exec("DROP TRIGGER reject_hook"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				result, err := s.Hook(ctx, repo, true, false)
				if err != nil || len(result) != 2 || !result[0].Installed || !result[1].Installed {
					t.Fatalf("retry failed: %+v %v", result, err)
				}
			}
			if _, err = s.Hook(ctx, repo, true, true); err != nil {
				t.Fatal(err)
			}
			if _, err = os.Lstat(path); !os.IsNotExist(err) {
				t.Fatal("uninstall failed", err)
			}
		})
	}
}

func TestHookRollbackPreservesConcurrentEdits(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hook")
	if err := os.WriteFile(path, []byte("owned"), 0700); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("user edit"), 0700); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err = rollbackCreatedHook(root, filepath.Base(path), info, []byte("owned")); err == nil {
		t.Fatal("changed hook was removed")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != "user edit" {
		t.Fatal("user edit was not preserved", err)
	}
}
