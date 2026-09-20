package core

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommittedSourceIgnoresLocalReplacementObjects(t *testing.T) {
	ctx := context.Background()
	config := "version=1\n[checks.original]\ncommand=\"echo original\"\n"
	s, repo := fixture(t, config)
	original, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{ProjectFile: "version=1\n[checks.replacement]\ncommand=\"echo replacement\"\n", "source.txt": "replacement", ".gitattributes": "*.dat filter=lfs\n"} {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"add", "."}, {"-c", "commit.gpgsign=false", "commit", "-m", "replacement"}} {
		if _, err := Git(ctx, repo, args...); err != nil {
			t.Fatal(err)
		}
	}
	replacement, err := ResolveCommit(ctx, repo, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Git(ctx, repo, "update-ref", "refs/replace/"+original, replacement); err != nil {
		t.Fatal(err)
	}
	// Ordinary Git proves that the replacement fixture really changes the view.
	ordinary := exec.Command("git", "-C", repo, "show", original+":source.txt")
	if b, err := ordinary.Output(); err != nil || strings.TrimSpace(string(b)) != "replacement" {
		t.Fatalf("replacement fixture: %q, %v", b, err)
	}
	plan, err := s.Plan(ctx, repo, original)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Commit != original || plan.Config.Checks["original"].Command != "echo original" || len(plan.Checks) != 1 {
		t.Fatalf("replacement changed receipt/config: %+v", plan)
	}
	workspace, err := s.Store.Prepare(ctx, plan)
	if err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{ProjectFile: config, "source.txt": "committed"} {
		b, err := os.ReadFile(filepath.Join(workspace, name))
		if err != nil || string(b) != want {
			t.Fatalf("replacement changed prepared %s: %q, %v", name, b, err)
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, ".gitattributes")); !os.IsNotExist(err) {
		t.Fatal("replacement attributes reached workspace")
	}
	got, err := Git(ctx, repo, "rev-parse", "refs/replace/"+original)
	if err != nil || got != replacement {
		t.Fatal("source replacement ref was changed")
	}
	// Blob replacements must also be ignored by direct streamed object readers.
	blob, err := Git(ctx, repo, "rev-parse", original+":source.txt")
	if err != nil {
		t.Fatal(err)
	}
	replacementBlob, err := Git(ctx, repo, "rev-parse", replacement+":source.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Git(ctx, repo, "update-ref", "refs/replace/"+blob, replacementBlob); err != nil {
		t.Fatal(err)
	}
	got, err = readAttributeBlob(ctx, repo, blob)
	if err != nil || got != "committed" {
		t.Fatalf("raw reader honored replacement: %q, %v", got, err)
	}
}
