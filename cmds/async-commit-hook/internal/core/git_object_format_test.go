package core

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkspacePreservesSourceObjectFormat(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			s, repo := fixtureWithObjectFormat(t, "version=1\n[checks.ok]\ncommand=\"exit 0\"\n", format)
			ctx := context.Background()
			plan, err := s.Plan(ctx, repo, "")
			if err != nil {
				t.Fatal(err)
			}
			length := 40
			if format == "sha256" {
				length = 64
			}
			if len(plan.Commit) != length {
				t.Fatalf("unexpected %s commit: %s", format, plan.Commit)
			}
			if err := os.WriteFile(filepath.Join(repo, "source.txt"), []byte("uncommitted"), 0600); err != nil {
				t.Fatal(err)
			}
			workspace, err := s.Store.Prepare(ctx, plan)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := Git(ctx, workspace, "rev-parse", "--show-object-format=storage"); err != nil || got != format {
				t.Fatalf("workspace format %q: %v", got, err)
			}
			if got, err := ResolveCommit(ctx, workspace, "HEAD"); err != nil || got != plan.Commit {
				t.Fatalf("workspace commit %q: %v", got, err)
			}
			if got, err := os.ReadFile(filepath.Join(workspace, "source.txt")); err != nil || string(got) != "committed" {
				t.Fatalf("workspace source %q: %v", got, err)
			}
			run := runFixture(t, s, repo)
			if run.Commit != plan.Commit || run.State != Passed {
				t.Fatalf("execution failed: %+v", run)
			}
			if got, err := os.ReadFile(filepath.Join(repo, "source.txt")); err != nil || string(got) != "uncommitted" {
				t.Fatalf("original worktree changed: %q: %v", got, err)
			}
		})
	}
}
