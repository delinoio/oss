package core

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"testing"
)

func TestOrdinaryHookUninstallRemovesEveryOwnedHook(t *testing.T) {
	for _, missing := range []bool{false, true} {
		s, repo := fixture(t, "version=1\n")
		ctx := context.Background()
		items, err := s.Hook(ctx, repo, true, false)
		if err != nil || len(items) != 2 {
			t.Fatalf("install: %+v %v", items, err)
		}
		if missing {
			if err = os.Remove(items[1].Path); err != nil {
				t.Fatal(err)
			}
		}
		for attempt := 0; attempt < 2; attempt++ {
			if _, err = s.Hook(ctx, repo, false, true); err != nil {
				t.Fatal(err)
			}
			for _, item := range items {
				if _, err = os.Lstat(item.Path); !os.IsNotExist(err) {
					t.Fatalf("owned hook survived uninstall: %s %v", item.Path, err)
				}
				if _, err = s.installation("hook:" + item.Path); !errors.Is(err, sql.ErrNoRows) {
					t.Fatalf("ownership survived uninstall: %s %v", item.Path, err)
				}
			}
		}
	}
}

func TestOrdinaryHookUninstallPreservesUnrelatedAndModifiedHooks(t *testing.T) {
	for _, owned := range []bool{false, true} {
		s, repo := fixture(t, "version=1\n")
		ctx := context.Background()
		if _, err := s.Hook(ctx, repo, owned, false); err != nil {
			t.Fatal(err)
		}
		path, err := hookPath(ctx, repo, "pre-push")
		if err != nil {
			t.Fatal(err)
		}
		if !owned {
			if _, err = os.Lstat(path); !os.IsNotExist(err) {
				t.Fatalf("pre-push installed without opt-in: %v", err)
			}
		}
		user := []byte("#!/bin/sh\necho user-owned work\n")
		if err = os.WriteFile(path, user, 0755); err != nil {
			t.Fatal(err)
		}
		_, err = s.Hook(ctx, repo, false, true)
		if (err != nil) != owned {
			t.Fatalf("only a modified owned hook should report conflict: owned=%t err=%v", owned, err)
		}
		got, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(got, user) {
			t.Fatalf("user hook was changed: %q %v", got, err)
		}
	}
}
