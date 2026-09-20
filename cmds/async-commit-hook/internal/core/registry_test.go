package core

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestExplicitRegistrationAfterCheckoutPathReusePreservesHistory(t *testing.T) {
	for _, separate := range []bool{false, true} {
		t.Run(map[bool]string{false: "same-common-path", true: "different-common-path"}[separate], func(t *testing.T) {
			s, path := fixture(t, "version=1\n")
			ctx := context.Background()
			oldReceipt, err := s.Submit(ctx, path, "", false)
			if err != nil {
				t.Fatal(err)
			}
			oldRun, err := s.Store.Run(oldReceipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(path, path+"-old"); err != nil {
				t.Fatal(err)
			}
			if err = os.Mkdir(path, 0700); err != nil {
				t.Fatal(err)
			}
			args := []string{"init", "--quiet", "--template="}
			if separate {
				args = append(args, "--separate-git-dir", filepath.Join(filepath.Dir(path), "other-git"))
			}
			if _, err = Git(ctx, path, args...); err != nil {
				t.Fatal(err)
			}
			if _, err = s.Plan(ctx, path, ""); err == nil {
				t.Fatal("replacement inherited trust")
			}
			if _, err = (&API{s: s}).worktree(oldRun.WorktreeID); err == nil {
				t.Fatal("old worktree exposed replacement source")
			}
			repo, wt, err := s.Init(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			if repo.ID == oldRun.RepositoryID || wt.ID == oldRun.WorktreeID {
				t.Fatal("replacement reused historical identity")
			}
			againRepo, againWT, err := s.Init(ctx, path)
			if err != nil || againRepo.ID != repo.ID || againWT.ID != wt.ID {
				t.Fatal("registration is not idempotent", err)
			}
			repos, err := s.Store.Repositories()
			if err != nil || len(repos) != 2 {
				t.Fatal("historical registry was lost", err)
			}
			for _, r := range repos {
				for _, w := range r.Worktrees {
					if w.Available != (r.ID == repo.ID) {
						t.Fatal("incorrect source availability")
					}
				}
			}
			saved, err := s.Store.Run(oldRun.ID)
			if err != nil || saved.RepositoryID != oldRun.RepositoryID || saved.WorktreeID != oldRun.WorktreeID {
				t.Fatal("history was rebound", err)
			}
		})
	}
}

func TestLegacyPathRegistryUpgradePreservesIDsAndRequiresExplicitTrust(t *testing.T) {
	root := t.TempDir()
	if err := PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	common := filepath.Join(root, "git")
	if err := os.Mkdir(common, 0700); err != nil {
		t.Fatal(err)
	}
	repo, wt, run := ID(), ID(), ID()
	db, err := sql.Open("sqlite", filepath.Join(root, "state.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE repositories(id TEXT PRIMARY KEY,common_dir TEXT UNIQUE NOT NULL,name TEXT NOT NULL);
 CREATE TABLE worktrees(id TEXT PRIMARY KEY,repository_id TEXT NOT NULL REFERENCES repositories(id),path TEXT UNIQUE NOT NULL,branch TEXT NOT NULL);
 PRAGMA user_version=1;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO repositories VALUES(?,?,?)", repo, common, "old"); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("INSERT INTO worktrees VALUES(?,?,?,?)", wt, repo, root, "old"); err != nil {
		t.Fatal(err)
	}
	db.Close()
	store, err := OpenStore(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	// Existing identities survive the table rewrite and remain valid FK targets.
	r := Run{ID: run, RepositoryID: repo, WorktreeID: wt, State: Queued, Checks: []Check{}}
	if _, err = store.InsertRun(&r, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err = store.Registered(common, root); err == nil {
		t.Fatal("legacy path alone established trust")
	}
	replacement, _, err := store.Register(common, root, "new")
	if err != nil || replacement.ID == repo {
		t.Fatal("explicit registration failed", err)
	}
	stored, err := store.Run(run)
	if err != nil || stored.RepositoryID != repo {
		t.Fatal("legacy identity lost", err)
	}
	var enabled int
	if err = store.DB.QueryRow("PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
		t.Fatal("foreign key enforcement disabled", err)
	}
}

func TestQueuedRunRejectsReusedCheckoutBeforePreparingSource(t *testing.T) {
	for _, initialized := range []bool{false, true} {
		t.Run(map[bool]string{false: "untrusted-replacement", true: "independently-trusted-replacement"}[initialized], func(t *testing.T) {
			s, path := fixture(t, "version=1\n[checks.test]\ncommand=\"echo validated\"\n")
			ctx := context.Background()
			receipt, err := s.Submit(ctx, path, "", false)
			if err != nil {
				t.Fatal(err)
			}
			original, err := s.Store.Run(receipt.RunID)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.Rename(path, path+"-old"); err != nil {
				t.Fatal(err)
			}
			if _, err = Git(ctx, filepath.Dir(path), "clone", "--quiet", "--no-hardlinks", "--", path+"-old", path); err != nil {
				t.Fatal(err)
			}
			if sha, err := ResolveCommit(ctx, path, original.Commit); err != nil || sha != original.Commit {
				t.Fatal("replacement lacks the accepted commit", err)
			}
			if initialized {
				if _, _, err := s.Init(ctx, path); err != nil {
					t.Fatal(err)
				}
			}
			loaded, err := s.Store.Run(original.ID)
			if err != nil {
				t.Fatal(err)
			}
			if dir, err := s.Store.Prepare(ctx, loaded); err == nil || dir != "" {
				t.Fatal("replacement source was prepared", dir, err)
			}
			if _, err := os.Stat(filepath.Join(s.Store.Root, "workspaces", loaded.ID)); !os.IsNotExist(err) {
				t.Fatal("workspace created before trust validation", err)
			}
			if err := s.RunOne(original.ID); err != nil {
				t.Fatal(err)
			}
			result, err := s.Store.Run(original.ID)
			if err != nil || result.State != Failed || result.RepositoryID != original.RepositoryID {
				t.Fatal("queued run used replacement source", result.State, err)
			}
			if _, _, err := s.Init(ctx, path); err != nil {
				t.Fatal(err)
			}
			fresh := runFixture(t, s, path)
			if fresh.RepositoryID == original.RepositoryID || !s.GateRun(fresh).Passed {
				t.Fatal("explicit replacement registration did not permit a fresh attempt")
			}
		})
	}
}
