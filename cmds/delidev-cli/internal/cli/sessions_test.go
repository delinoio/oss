package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/server"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLISessionAcceptanceQueueAndArchive(t *testing.T) {
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DataDir: root, Listen: "127.0.0.1:0", Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-ready:
	case <-time.After(10 * time.Second):
		t.Fatal("server startup timeout")
	}
	run := func(args []string, input any) map[string]any {
		t.Helper()
		raw := ""
		if input != nil {
			b, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			raw = string(b)
		}
		code, result := cliRun(t, root, args, raw)
		if code != 0 {
			t.Fatalf("%v: %d %v", args, code, result)
		}
		return result["result"].(map[string]any)
	}
	grant := run([]string{"device", "create-pairing", "--type", "worker", "--name", "session fixture"}, nil)
	codeDocument, err := os.ReadFile(grant["code_file"].(string))
	if err != nil {
		t.Fatal(err)
	}
	workerRoot := filepath.Join(t.TempDir(), "worker")
	code, paired := cliRun(t, root, []string{"worker", "pair", "--worker-dir", workerRoot, "--code-stdin"}, string(codeDocument))
	if code != 0 {
		t.Fatal(paired)
	}
	machine := paired["result"].(map[string]any)["machine_id"].(string)
	p := run([]string{"provider", "create"}, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth})["resource"].(map[string]any)
	m := run([]string{"model", "create"}, domain.Model{Name: "Fixture model", NativeID: "fixture", ProviderID: domain.ID(p["id"].(string)), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})["resource"].(map[string]any)
	a := run([]string{"agent", "create"}, domain.Agent{Name: "Fixture", Harness: domain.Codex, ModelID: domain.ID(m["id"].(string)), Options: domain.AgentOptions{Permission: domain.PermissionDefault}})["resource"].(map[string]any)
	request := string(domain.NewID())
	args := []string{"session", "create", "--request-id", request}
	input := domain.CreateSession{Name: "CLI session", AgentID: domain.ID(a["id"].(string)), MachineID: domain.ID(machine), Workspace: domain.GeneralChat, Prompt: "CLI private fixture"}
	created := run(args, input)
	session := created["session"].(map[string]any)
	id := session["id"].(string)
	if body := session["data"].(map[string]any); body["source"] != "EXTERNAL_CLI" || body["dispatch"] != "blocked" || body["outcome"] != "not-started" {
		t.Fatal("CLI changed acceptance semantics")
	}
	if replay := run(args, input); replay["replayed"] != true || replay["session"].(map[string]any)["id"] != id {
		t.Fatal("CLI acceptance duplicated")
	}
	enqueued := run([]string{"session", "enqueue", "--id", id}, domain.SessionInput{Prompt: "plan later", Mode: domain.PlanMode})
	item := enqueued["input"].(map[string]any)
	itemID := item["id"].(string)
	run([]string{"queue", "edit", "--session-id", id, "--id", itemID, "--revision", "1"}, map[string]string{"prompt": "changed plan"})
	listed := run([]string{"queue", "list", "--session-id", id}, nil)["inputs"].([]any)
	if len(listed) != 2 || listed[1].(map[string]any)["data"].(map[string]any)["mode"] != "plan" {
		t.Fatal("CLI edit changed queued mode")
	}
	removed := run([]string{"queue", "remove", "--session-id", id, "--id", itemID, "--revision", "2"}, nil)
	session = removed["session"].(map[string]any)
	revision := strconv.FormatUint(uint64(session["revision"].(float64)), 10)
	archived := run([]string{"session", "archive", "--id", id, "--revision", revision}, nil)["session"].(map[string]any)
	if len(run([]string{"session", "list"}, nil)["sessions"].([]any)) != 0 {
		t.Fatal("CLI ordinary list includes archive")
	}
	revision = strconv.FormatUint(uint64(archived["revision"].(float64)), 10)
	restored := run([]string{"session", "restore", "--id", id, "--revision", revision}, nil)["session"].(map[string]any)
	if restored["data"].(map[string]any)["dispatch"] != "paused" {
		t.Fatal("CLI restore dispatched queue")
	}
	revision = strconv.FormatUint(uint64(restored["revision"].(float64)), 10)
	code, failed := cliRun(t, root, []string{"session", "resume", "--id", id, "--revision", revision}, "")
	if code == 0 || failed["error"].(map[string]any)["code"] != "conflict" {
		t.Fatal("CLI resumed an unprepared workspace")
	}

	code, failed = cliRun(t, root, []string{"session", "recover-workspace", "--id", id, "--revision", revision, "--cleanup", "--wait"}, "")
	if code != 5 || failed["error"].(map[string]any)["code"] != "conflict" {
		t.Fatal("CLI recovered a confirmed canceled preparation without uncertainty")
	}
	workerCtx, stopWorker := context.WithCancel(ctx)
	workerReady, workerDone := make(chan struct{}), make(chan error, 1)
	go func() {
		workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Ready: func(domain.ID) { close(workerReady) }})
	}()
	defer func() {
		stopWorker()
		if err := <-workerDone; err != nil {
			t.Error(err)
		}
	}()
	select {
	case <-workerReady:
	case <-time.After(10 * time.Second):
		t.Fatal("Worker startup timed out")
	}
	prepareArgs := []string{"session", "prepare", "--id", id, "--revision", revision, "--wait", "--request-id", string(domain.NewID())}
	prepared := run(prepareArgs, nil)
	session = prepared["session"].(map[string]any)
	preparedJob := prepared["workspace_job"].(map[string]any)
	if state := session["data"].(map[string]any); state["preparation"].(map[string]any)["state"] != "ready" || state["dispatch"] != "paused" || state["outcome"] != "not-started" {
		t.Fatal("preparation changed execution or failed to publish readiness")
	}
	chat := preparedJob["data"].(map[string]any)["output"].(map[string]any)["primary_path"].(string)
	retained := filepath.Join(chat, "retained.txt")
	if err := os.WriteFile(retained, []byte("preserve across archive"), 0600); err != nil {
		t.Fatal(err)
	}
	replay := run(prepareArgs, nil)
	if replay["replayed"] != true || replay["workspace_job"].(map[string]any)["id"] != preparedJob["id"] {
		t.Fatal("preparation receipt repeated native work")
	}
	run([]string{"session", "archive", "--id", id, "--revision", strconv.FormatUint(uint64(session["revision"].(float64)), 10)}, nil)
	if raw, err := os.ReadFile(retained); err != nil || string(raw) != "preserve across archive" {
		t.Fatal("Archive deleted ready workspace")
	}

	// Create two real repositories through validated CLI writes. Explicit full
	// commits exercise detached preparation without network or user Git accounts.
	repositories := []domain.ID{}
	commits := []string{}
	for i := 0; i < 2; i++ {
		checkout := t.TempDir()
		git := func(args ...string) string {
			t.Helper()
			argv := append([]string{"-C", checkout, "-c", "user.name=DeliDev Fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgSign=false", "-c", "core.hooksPath=" + t.TempDir()}, args...)
			out, err := exec.Command("git", argv...).CombinedOutput()
			if err != nil {
				t.Fatalf("git: %v %s", err, out)
			}
			return strings.TrimSpace(string(out))
		}
		git("init", "-b", "main")
		if err := os.WriteFile(filepath.Join(checkout, "tracked.txt"), []byte("fixture"), 0600); err != nil {
			t.Fatal(err)
		}
		git("add", "tracked.txt")
		git("commit", "-m", "fixture")
		commit := git("rev-parse", "HEAD")
		commits = append(commits, commit)
		saved := run([]string{"repository", "create", "--wait"}, domain.Repository{Name: "fixture-" + strconv.Itoa(i), Checkouts: []domain.Checkout{{MachineID: domain.ID(machine), Path: checkout}}, Starting: domain.Reference{Type: domain.CommitReference, Name: commit}})["resource"].(map[string]any)
		repositories = append(repositories, domain.ID(saved["id"].(string)))
	}
	project := run([]string{"project", "create"}, domain.Project{Name: "all repositories", Repositories: repositories, PrimaryRepository: repositories[1]})["resource"].(map[string]any)
	selected := input
	selected.ProjectID = domain.ID(project["id"].(string))
	selected.Workspace = domain.Worktree
	worktree := run([]string{"session", "create", "--wait"}, selected)
	workspaceJob := worktree["workspace_job"].(map[string]any)
	output := workspaceJob["data"].(map[string]any)["output"].(map[string]any)
	preparedRepos := output["repositories"].([]any)
	if len(preparedRepos) != 2 || output["primary_path"] != preparedRepos[1].(map[string]any)["path"] {
		t.Fatal("primary/ordered repositories were lost")
	}
	for i, item := range preparedRepos {
		data := item.(map[string]any)
		if data["id"] != string(repositories[i]) || data["starting_commit"] != commits[i] || data["owned"] != true {
			t.Fatal("wrong preparation identity/commit/ownership")
		}
		path := data["path"].(string)
		out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) != commits[i] {
			t.Fatal("recorded commit differs from actual checkout")
		}
		out, err = exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) != "HEAD" {
			t.Fatal("prepared worktree was not detached")
		}
	}
}
