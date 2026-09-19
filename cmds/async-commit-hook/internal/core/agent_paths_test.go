package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProjectAgentsResolveNestedAndLinkedWorktreeRoots(t *testing.T) {
	s, repo := fixture(t, "version=1\n")
	linked := filepath.Join(t.TempDir(), "linked")
	if _, err := Git(context.Background(), repo, "worktree", "add", "--detach", linked, "HEAD"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Init(context.Background(), linked); err != nil {
		t.Fatal(err)
	}
	for _, root := range []string{repo, linked} {
		_, canonical, _, err := Discover(context.Background(), root)
		if err != nil {
			t.Fatal(err)
		}
		nested := filepath.Join(root, "src", "nested")
		if err := os.MkdirAll(nested, 0700); err != nil {
			t.Fatal(err)
		}
		for _, client := range []string{"codex", "claude-code", "opencode"} {
			installed, err := s.Agent(client, "project", nested, false)
			if err != nil {
				t.Fatal(client, err)
			}
			if !strings.HasPrefix(installed.Path, canonical+string(filepath.Separator)) || strings.Contains(installed.Path, "nested") {
				t.Fatal("wrong project root", installed.Path)
			}
			again, err := s.Agent(client, "project", root, false)
			if err != nil || again.Path != installed.Path {
				t.Fatal("root/subdirectory ownership differs", err)
			}
			if _, err := s.Agent(client, "project", nested, true); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Agent(client, "project", root, true); err != nil {
				t.Fatal(err)
			}
		}
		entries, err := os.ReadDir(nested)
		if err != nil || len(entries) != 0 {
			t.Fatal("wrote integration under subdirectory", err)
		}
	}
}
