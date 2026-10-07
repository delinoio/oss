package cli

import (
	"bytes"
	"context"
	"encoding/json"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/testgit"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestCLISessionAcceptanceQueueAndArchive(t *testing.T) {
	gitExecutable, registerGit := testgit.Setup(t, map[string]string{})
	t.Setenv("PATH", filepath.Dir(gitExecutable)+string(os.PathListSeparator)+os.Getenv("PATH"))
	root := filepath.Join(t.TempDir(), "server")
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	var fixtureLogs bytes.Buffer
	// One handler serializes both services; inspect the buffer only after the
	// owned Worker and server have joined, including on an assertion failure.
	logger := slog.New(slog.NewJSONHandler(&fixtureLogs, nil))
	done := make(chan error, 1)
	go func() {
		done <- server.Serve(ctx, server.Config{DisableBackgroundMaintenanceForTesting: true, DataDir: root, Listen: "127.0.0.1:0", Logger: logger}, func(server.Endpoint) { close(ready) })
	}()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if t.Failed() {
			t.Logf("Private fixture server and Worker log:\n%s", fixtureLogs.String())
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
	p := run([]string{"provider", "create"}, domain.Provider{Name: "Fixture", Endpoint: "http://127.0.0.1:1/v1", Protocol: domain.OpenAIChat, Authentication: domain.KeylessAuth, Enabled: new(true)})["resource"].(map[string]any)
	m := run([]string{"model", "create"}, domain.Model{Name: "Fixture model", NativeID: "fixture", ProviderID: domain.ID(p["id"].(string)), Harnesses: []domain.Harness{domain.Codex}, MetadataSource: domain.UserDeclared})["resource"].(map[string]any)
	account := run([]string{"account", "create"}, domain.Account{Alias: "Fixture account", Type: domain.APIAccount, ProviderID: domain.ID(p["id"].(string)), Enabled: true, Health: domain.AccountDisconnected})["resource"].(map[string]any)
	a := run([]string{"agent", "create", "--model-revision", "1"}, domain.Agent{Name: "Fixture", Accounts: []domain.WeightedAccount{{ID: domain.ID(account["id"].(string)), Weight: 1}}, Harness: domain.Codex, ModelID: domain.ID(m["id"].(string)), Options: domain.AgentOptions{Permission: domain.PermissionDefault}})["resource"].(map[string]any)
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
		workerDone <- worker.Run(workerCtx, worker.Config{Root: workerRoot, Logger: logger, Ready: func(domain.ID) { close(workerReady) }})
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
	readRoots := run([]string{"session", "files", "roots", "--id", id}, nil)["roots"].([]any)
	if len(readRoots) != 1 || readRoots[0].(map[string]any)["name"] != "General Chat" {
		t.Fatal("CLI lost projectless root")
	}
	listing := run([]string{"session", "files", "list", "--id", id}, nil)["entries"].([]any)
	if len(listing) != 1 || listing[0].(map[string]any)["name"] != "retained.txt" {
		t.Fatal("CLI did not read the Worker directory")
	}
	preview := run([]string{"session", "files", "read", "--id", id, "--path", "retained.txt"}, nil)
	if preview["text"] != "preserve across archive" {
		t.Fatal("CLI did not return original Worker file content")
	}
	replay := run(prepareArgs, nil)
	if replay["replayed"] != true || replay["workspace_job"].(map[string]any)["id"] != preparedJob["id"] {
		t.Fatal("preparation receipt repeated native work")
	}
	run([]string{"session", "archive", "--id", id, "--revision", strconv.FormatUint(uint64(session["revision"].(float64)), 10)}, nil)
	if raw, err := os.ReadFile(retained); err != nil || string(raw) != "preserve across archive" {
		t.Fatal("Archive deleted ready workspace")
	}

	if run([]string{"session", "files", "read", "--id", id, "--path", "retained.txt"}, nil)["text"] != "preserve across archive" {
		t.Fatal("archive hid retained files")
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
		url := "https://github.com/fixture/repo-" + strconv.Itoa(i) + ".git"
		git("remote", "add", "origin", url)
		registerGit(url, checkout)
		checkouts := []domain.Checkout{}
		if i == 1 {
			checkouts = append(checkouts, domain.Checkout{MachineID: domain.ID(machine), Path: checkout})
		}
		saved := run([]string{"repository", "create", "--wait"}, domain.Repository{RemoteURL: url, Name: "fixture-" + strconv.Itoa(i), Checkouts: checkouts, Starting: domain.Reference{Type: domain.CommitReference, Name: commit}})["resource"].(map[string]any)
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
	worktreeID := worktree["session"].(map[string]any)["id"].(string)
	fileRoots := run([]string{"session", "files", "roots", "--id", worktreeID}, nil)["roots"].([]any)
	if len(fileRoots) != 2 || fileRoots[1].(map[string]any)["primary"] != true {
		t.Fatal("CLI lost nonfirst primary root")
	}
	for _, repository := range repositories {
		preview := run([]string{"session", "files", "read", "--id", worktreeID, "--repository-id", string(repository), "--path", "tracked.txt"}, nil)
		if preview["text"] != "fixture" {
			t.Fatal("CLI did not read every prepared worktree")
		}
	}
	var comments []map[string]any
	var commentInputs []domain.CreateReviewComment
	var commentRequests []string
	for i, item := range preparedRepos {
		data := item.(map[string]any)
		if data["id"] != string(repositories[i]) || data["starting_commit"] != commits[i] || data["owned"] != true {
			t.Fatal("wrong preparation identity/commit/ownership")
		}
		path := data["path"].(string)
		if err := os.WriteFile(filepath.Join(path, "tracked.txt"), []byte("CLI diff change\n"), 0600); err != nil {
			t.Fatal(err)
		}
		// Keep this two-root fixture to one diff read and one review-context
		// read total. Each observation starts bounded Git processes and expires
		// after 15 seconds; repeating both across both roots flakes under the
		// Windows full-suite load. Expand it when Windows observations reliably
		// fit that contract deadline under full-suite load.
		var comparison map[string]any
		if i == 0 {
			comparison = run([]string{"session", "diff", "--id", worktreeID, "--repository-id", string(repositories[i]), "--comparison", "creation"}, nil)["diff"].(map[string]any)
			if comparison["base_object"] != commits[i] || comparison["head_commit"] != commits[i] || !strings.Contains(comparison["patch"].(string), "+CLI diff change") {
				t.Fatal("CLI diff lost selected creation commit or repository")
			}
		} else {
			review := run([]string{"session", "review-context", "--id", worktreeID, "--repository-id", string(repositories[i]), "--comparison", "creation"}, nil)
			comparison = review["diff"].(map[string]any)
			files := review["files"].([]any)
			if len(files) != 1 || !strings.Contains(comparison["patch"].(string), "+CLI diff change") {
				t.Fatal("review lost original diff identity")
			}
			file := files[0].(map[string]any)
			lines := file["lines"].([]any)
			if file["path"] != "tracked.txt" || file["kind"] != "text" || len(lines) != 2 || lines[0].(map[string]any)["newline"] != false || lines[1].(map[string]any)["new"] != float64(1) {
				t.Fatal("review lost original line sides or EOF")
			}
		}
		commentInput := domain.CreateReviewComment{Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceGitDiff, RepositoryID: repositories[i], Path: ".", Comparison: domain.DiffCreation}, DiffRevision: comparison["revision"].(string), Selection: domain.ReviewSelection{Path: "tracked.txt", Kind: domain.ReviewLineAnchor, Side: domain.ReviewNewSide, Start: 1, End: 1}, Body: "Please revise repository " + strconv.Itoa(i)}
		requestID := string(domain.NewID())
		created := run([]string{"session", "review", "create", "--id", worktreeID, "--request-id", requestID}, commentInput)["comment"].(map[string]any)
		comments = append(comments, created)
		commentInputs = append(commentInputs, commentInput)
		commentRequests = append(commentRequests, requestID)
		out, err := exec.Command("git", "-C", path, "rev-parse", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) != commits[i] {
			t.Fatal("recorded commit differs from actual checkout")
		}
		out, err = exec.Command("git", "-C", path, "rev-parse", "--abbrev-ref", "HEAD").Output()
		if err != nil || strings.TrimSpace(string(out)) != "HEAD" {
			t.Fatal("prepared worktree was not detached")
		}
	}
	if err := os.WriteFile(filepath.Join(preparedRepos[0].(map[string]any)["path"].(string), "tracked.txt"), []byte("changed after comment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	selection := domain.SubmitReviewComments{Mode: domain.PlanMode}
	for _, c := range comments {
		selection.Comments = append(selection.Comments, domain.ReviewCommentRef{ID: domain.ID(c["id"].(string)), Revision: uint64(c["revision"].(float64))})
	}
	encoded, _ := json.Marshal(selection)
	code, stale := cliRun(t, root, []string{"session", "review", "submit", "--id", worktreeID}, string(encoded))
	if code != 5 || stale["error"].(map[string]any)["code"] != string(domain.Conflict) {
		t.Fatal("stale review submitted implicitly", stale)
	}
	selection.AllowStale = true
	submissionRequest := string(domain.NewID())
	submitArgs := []string{"session", "review", "submit", "--id", worktreeID, "--request-id", submissionRequest}
	submitted := run(submitArgs, selection)
	submission := submitted["submission"].(map[string]any)
	details := submission["data"].(map[string]any)["submission"].(map[string]any)
	selectedComments := details["comments"].([]any)
	if submission["id"] != submissionRequest || selectedComments[0].(map[string]any)["freshness"] != "stale" || selectedComments[1].(map[string]any)["freshness"] != "current" {
		t.Fatal("submission lost exact freshness", details)
	}
	change := submitted["change"].(map[string]any)
	queued := change["input"].(map[string]any)
	queuedBody := queued["data"].(map[string]any)
	if queuedBody["mode"] != "plan" || queuedBody["delivery"] != "queued" || !strings.Contains(queuedBody["prompt"].(string), commentInputs[0].Body) || !strings.Contains(queuedBody["prompt"].(string), commentInputs[1].Body) {
		t.Fatal("review did not use original queue rules")
	}
	listedReviews := run([]string{"session", "review", "list", "--id", worktreeID}, nil)["reviews"].([]any)
	if len(listedReviews) != 3 {
		t.Fatal("review list lost comments or submission")
	}
	current := run([]string{"session", "review", "get", "--id", worktreeID, "--review-id", comments[0]["id"].(string)}, nil)
	currentRevision := strconv.FormatUint(uint64(current["revision"].(float64)), 10)
	edited := run([]string{"session", "review", "edit", "--id", worktreeID, "--review-id", comments[0]["id"].(string), "--revision", currentRevision}, map[string]string{"body": "Edited after submission"})["comment"].(map[string]any)
	stopWorker()
	replayedCreate := run([]string{"session", "review", "create", "--id", worktreeID, "--request-id", commentRequests[0]}, commentInputs[0])
	if replayedCreate["replayed"] != true || replayedCreate["comment"].(map[string]any)["revision"] != edited["revision"] {
		t.Fatal("comment replay reread files or rewrote later edit")
	}
	deleteArgs := []string{"session", "review", "delete", "--id", worktreeID, "--review-id", edited["id"].(string), "--revision", strconv.FormatUint(uint64(edited["revision"].(float64)), 10), "--request-id", string(domain.NewID())}
	run(deleteArgs, nil)
	if run(deleteArgs, nil)["replayed"] != true {
		t.Fatal("comment deletion did not replay")
	}
	replayedSubmit := run(submitArgs, selection)
	if replayedSubmit["replayed"] != true || replayedSubmit["submission"].(map[string]any)["id"] != submission["id"] || replayedSubmit["change"].(map[string]any)["input"].(map[string]any)["id"] != queued["id"] {
		t.Fatal("submission replay lost accepted input after comment deletion")
	}
	if len(run([]string{"queue", "list", "--session-id", worktreeID}, nil)["inputs"].([]any)) != 2 {
		t.Fatal("review retry duplicated queued input")
	}
}
