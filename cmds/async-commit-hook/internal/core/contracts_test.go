//go:build !windows

package core

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func commitFixture(t *testing.T, repo string) {
	t.Helper()
	for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "--quiet", "--allow-empty", "-m", "next"}} {
		if _, err := Git(context.Background(), repo, args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSourceRefusesSubmoduleAndLFSBeforeCommands(t *testing.T) {
	for _, kind := range []string{"submodule", "attributes", "pointer"} {
		t.Run(kind, func(t *testing.T) {
			s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"echo must-not-run\"\n")
			switch kind {
			case "submodule":
				sha, _ := ResolveCommit(context.Background(), repo, "HEAD")
				if _, err := Git(context.Background(), repo, "update-index", "--add", "--cacheinfo", "160000,"+sha+",module"); err != nil {
					t.Fatal(err)
				}
			case "attributes":
				os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("*.bin filter=lfs\n"), 0600)
			case "pointer":
				os.WriteFile(filepath.Join(repo, "asset.bin"), []byte("version https://git-lfs.github.com/spec/v1\noid sha256:"+strings.Repeat("0", 64)+"\nsize 1\n"), 0600)
			}
			if kind == "submodule" {
				// Keep the synthetic gitlink in the index; git add would remove an
				// absent worktree directory before this unsupported-source check.
				if _, err := Git(context.Background(), repo, "-c", "commit.gpgsign=false", "commit", "--quiet", "-m", "gitlink"); err != nil {
					t.Fatal(err)
				}
			} else {
				commitFixture(t, repo)
			}
			receipt, err := s.Submit(context.Background(), repo, "", false)
			if err == nil {
				if err = s.RunOne(receipt.RunID); err != nil {
					t.Fatal(err)
				}
				r, _ := s.Store.Run(receipt.RunID)
				if r.State != Failed || r.Checks[0].StartedAt != nil {
					t.Fatalf("unsupported source executed: %+v", r)
				}
			}
		})
	}
}

func TestRawCheckoutDoesNotInvokeSourceFilters(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"test $(cat source.txt) = committed\"\n")
	marker := filepath.Join(t.TempDir(), "filter-ran")
	os.WriteFile(filepath.Join(repo, ".gitattributes"), []byte("source.txt filter=hostile\n"), 0600)
	commitFixture(t, repo)
	if _, err := Git(context.Background(), repo, "config", "filter.hostile.smudge", "touch '"+marker+"'; cat"); err != nil {
		t.Fatal(err)
	}
	r := runFixture(t, s, repo)
	if !s.GateRun(r).Passed {
		t.Fatalf("raw checkout failed: %+v", r)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("source checkout filter executed")
	}
}

func TestComparisonAndEnvironmentCompatibility(t *testing.T) {
	s, repo := fixture(t, `version=1
[checks.test]
command="cp source.txt report.xml"
[[checks.test.environment]]
name="ACH_TEST_INPUT"
required=true
[[checks.test.reports]]
kind="junit"
path="report.xml"
`)
	t.Setenv("ACH_TEST_INPUT", "one")
	os.WriteFile(filepath.Join(repo, "source.txt"), []byte(`<testsuite><testcase name="old"><failure/></testcase><testcase name="shared"><failure/></testcase></testsuite>`), 0600)
	commitFixture(t, repo)
	a := runFixture(t, s, repo)
	os.WriteFile(filepath.Join(repo, "source.txt"), []byte(`<testsuite><testcase name="new"><failure/></testcase><testcase name="shared"><failure/></testcase></testsuite>`), 0600)
	commitFixture(t, repo)
	b := runFixture(t, s, repo)
	comparison, err := s.Compare(b.ID, "")
	if err != nil || !comparison.Available || comparison.PreviousID != a.ID || len(comparison.New) != 1 || len(comparison.Continuing) != 1 || len(comparison.Resolved) != 1 {
		t.Fatalf("comparison %+v %v", comparison, err)
	}
	t.Setenv("ACH_TEST_INPUT", "two")
	gate, err := s.Gate(context.Background(), repo, "")
	if err != nil || gate.RunID != "" {
		t.Fatalf("changed declared input reused result: %+v %v", gate, err)
	}
	p, err := s.Plan(context.Background(), repo, "")
	if err != nil {
		t.Fatal(err)
	}
	b.Fingerprint = p.Fingerprint
	s.Store.SaveRun(b)
	comparison, err = s.Compare(b.ID, a.ID)
	if err != nil || comparison.Available {
		t.Fatal("incompatible comparison accepted")
	}
}

func TestOptionalFailureDoesNotFailRequiredGate(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.required]\ncommand=\"true\"\n[checks.optional]\ncommand=\"exit 7\"\noptional=true\n")
	r := runFixture(t, s, repo)
	if !s.GateRun(r).Passed {
		t.Fatalf("optional failure blocked required checks: %+v", r)
	}
	for _, c := range r.Checks {
		if c.Name == "optional" && c.State != Failed {
			t.Fatal("optional failure hidden")
		}
	}
}

func TestStorageFailureReapsOwnedCommands(t *testing.T) {
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"sleep 30\"\n")
	receipt, err := s.Submit(context.Background(), repo, "", false)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- s.RunOne(receipt.RunID) }()
	r := awaitCheck(t, s, receipt.RunID, Running)
	process := r.Checks[0].Process
	s.Store.Close()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("storage failure hidden")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("storage failure stranded command")
	}
	if ProcessAlive(process) {
		t.Fatal(fmt.Sprintf("owned process %d survived storage failure", process.PID))
	}
}

func TestCleanupFailureRemainsDurableAndFailsGate(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("requires unprivileged filesystem permissions")
	}
	s, repo := fixture(t, "version=1\n[checks.test]\ncommand=\"mkdir locked; touch locked/file; chmod 500 locked\"\n")
	r := runFixture(t, s, repo)
	defer os.Chmod(filepath.Join(s.Store.Root, "workspaces", r.ID, "locked"), 0700)
	found := false
	for _, d := range r.Diagnostics {
		found = found || d.Code == "workspace-cleanup-failed"
	}
	if !found || s.GateRun(r).Passed {
		t.Fatalf("cleanup failure hidden: %+v", r)
	}
	if r.Checks[0].Log.ID == "" {
		t.Fatal("cleanup lost collected evidence")
	}
}

func TestCustomLefthookPathIsPreservedWithExecutableExample(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	dir := filepath.Join(t.TempDir(), "shared-hooks")
	os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, "post-commit")
	original := []byte("#!/bin/sh\nexec lefthook run post-commit\n")
	os.WriteFile(path, original, 0700)
	if _, err := Git(context.Background(), repo, "config", "core.hooksPath", dir); err != nil {
		t.Fatal(err)
	}
	result, err := s.Hook(context.Background(), repo, false, false)
	if err != nil || len(result) != 1 || !strings.Contains(result[0].Example, "async-commit-hook:") || !strings.Contains(result[0].Manual, "--automatic") {
		t.Fatalf("manual integration %+v %v", result, err)
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(original) {
		t.Fatal("Lefthook hook overwritten")
	}
}
