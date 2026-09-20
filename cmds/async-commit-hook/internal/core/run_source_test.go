package core

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"unicode/utf8"
)

func TestRunLoadsRawSourceAndBranchOutsideJSONSnapshot(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo test\"\n")
	plan, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	raw := repo + string([]byte{0xff})
	branch := "branch-" + string([]byte{0xfe})
	if _, err = s.Store.DB.Exec("UPDATE worktrees SET path=? WHERE id=?", raw, plan.WorktreeID); err != nil {
		t.Fatal(err)
	}
	plan.Source, plan.Branch = raw, branch
	if _, err = s.Store.InsertRun(&plan, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.Store.Close(); err != nil {
		t.Fatal(err)
	}
	s.Store, err = OpenStore(s.Personal.StateDir)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		loaded, err := s.Store.Run(plan.ID)
		if err != nil || loaded.Source != raw || loaded.Branch != branch {
			t.Fatalf("raw identity lost: source=%q branch=%q error=%v", loaded.Source, loaded.Branch, err)
		}
		var display Run
		if err = json.Unmarshal(Encode(loaded), &display); err != nil || !utf8.ValidString(display.Source) || display.Source == raw {
			t.Fatalf("display should remain safe JSON: %+v %v", display, err)
		}
		// A subsequent state write must not make the lossy display authoritative.
		if err = s.Store.SaveRun(loaded); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRunAndRerunFromInvalidUTF8WorktreePath(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("Linux permits arbitrary non-NUL path bytes; other supported filesystems do not")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"test $(cat source.txt) = committed\"\n")
	raw := filepath.Join(filepath.Dir(repo), "raw-"+string([]byte{0xff}))
	if err := os.Rename(repo, raw); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Init(context.Background(), raw); err != nil {
		t.Fatal(err)
	}
	first := runFixture(t, s, raw)
	if first.Source != raw || !s.GateRun(first).Passed {
		t.Fatalf("run did not use the registered raw path: %+v", first)
	}
	receipt, err := s.Rerun(first.ID, false)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.RunOne(receipt.RunID); err != nil {
		t.Fatal(err)
	}
	rerun, err := s.Store.Run(receipt.RunID)
	if err != nil || rerun.Source != raw || !s.GateRun(rerun).Passed {
		t.Fatalf("rerun lost the original source: %+v %v", rerun, err)
	}
	if err = os.Rename(raw, repo); err != nil {
		t.Fatal(err)
	}
	if _, err = s.Store.Run(first.ID); err != nil {
		t.Fatalf("unavailable source prevented history access: %v", err)
	}
}
