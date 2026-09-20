package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
)

func TestPrePushJSONReturnsQueuedRunWithStartupError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("ACH_CONFIG", filepath.Join(home, "personal.toml"))
	paths, err := core.DefaultPaths()
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(home, "repo")
	if err := os.Mkdir(repo, 0700); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	git := func(args ...string) string {
		t.Helper()
		out, err := core.Git(ctx, repo, args...)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	git("init", "--quiet", "--template=")
	git("config", "user.email", "test@example.invalid")
	git("config", "user.name", "test")
	s, err := core.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, _, err := s.Init(ctx, repo); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, core.ProjectFile), []byte("version=1\n[checks.test]\ncommand=\"exit 0\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git("add", ".")
	git("-c", "commit.gpgsign=false", "commit", "-qm", "fixture")
	sha := git("rev-parse", "HEAD")
	if err := os.Mkdir(filepath.Join(paths.Control, "runner.log"), 0700); err != nil {
		t.Fatal(err)
	}
	var out, diagnostics bytes.Buffer
	input := fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", sha, strings.Repeat("0", 40))
	exit := Run(ctx, []string{"pre-push", "--policy", "run-and-wait", "--repo", repo, "--config", paths.Config, "--json"}, strings.NewReader(input), &out, &diagnostics)
	if exit != 3 {
		t.Fatalf("exit=%d: %s %s", exit, out.String(), diagnostics.String())
	}
	var response struct {
		SchemaVersion int              `json:"schema_version"`
		Result        []core.Gate      `json:"result"`
		Error         *core.Diagnostic `json:"error"`
	}
	decoder := json.NewDecoder(&out)
	if err := decoder.Decode(&response); err != nil {
		t.Fatal(err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		t.Fatal("stdout contains non-response output", err)
	}
	if response.SchemaVersion != 1 || response.Error == nil || len(response.Result) != 1 || diagnostics.Len() == 0 {
		t.Fatalf("missing response/error: %+v", response)
	}
	gate := response.Result[0]
	if gate.Commit != sha || gate.State != core.Queued || gate.Passed || !core.ValidID(gate.RunID) || len(gate.Diagnostics) != 1 || gate.Diagnostics[0].Code != "startup-failed" {
		t.Fatalf("missing accepted gate: %+v", gate)
	}
	run, err := s.Store.Run(gate.RunID)
	if err != nil || run.State != core.Queued || run.Commit != sha {
		t.Fatalf("not durable: %+v %v", run, err)
	}
}
