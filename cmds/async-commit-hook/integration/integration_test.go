//go:build !windows

package integration

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/async-commit-hook/internal/core"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

var binary string

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "ach-e2e-")
	if err != nil {
		panic(err)
	}
	binary = filepath.Join(dir, "ach")
	cmd := exec.Command("go", "build", "-o", binary, "..")
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err = cmd.Run(); err != nil {
		os.RemoveAll(dir)
		os.Exit(1)
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
func invoke(t *testing.T, config, repo string, args ...string) (core.Output, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	args = append(args, "--config", config, "--repo", repo, "--json")
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+filepath.Join(filepath.Dir(config), "home"))
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if err != nil {
		if e, ok := err.(*exec.ExitError); ok {
			exit = e.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	var response core.Output
	if err = json.Unmarshal(out.Bytes(), &response); err != nil {
		t.Fatalf("%v: invalid response: %s %s", args, out.String(), stderr.String())
	}
	return response, exit
}
func setup(t *testing.T, mode string) (string, string) {
	t.Helper()
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	os.Mkdir(repo, 0700)
	os.Mkdir(filepath.Join(root, "home"), 0700)
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()
	config := filepath.Join(root, "personal.toml")
	os.WriteFile(config, []byte(fmt.Sprintf("version=1\nmode=%q\napi_port=%d\nstate_dir=%q\n", mode, port, filepath.Join(root, "state"))), 0600)
	git(t, repo, "init", "--quiet")
	git(t, repo, "config", "user.name", "ach test")
	git(t, repo, "config", "user.email", "test@example.invalid")
	if r, exit := invoke(t, config, repo, "init"); exit != 0 {
		t.Fatalf("init %+v", r)
	}
	t.Cleanup(func() { invoke(t, config, repo, "daemon", "stop", "--force"); time.Sleep(300 * time.Millisecond) })
	os.WriteFile(filepath.Join(repo, core.ProjectFile), []byte(fmt.Sprintf("version=1\n[checks.test]\ncommand=%q\n", "while [ ! -f '"+filepath.Join(root, "release-check")+"' ]; do sleep 0.05; done; test $(cat source.txt) = committed; echo checked")), 0600)
	os.WriteFile(filepath.Join(repo, "source.txt"), []byte("committed"), 0600)
	return config, repo
}
func git(t *testing.T, repo string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", append([]string{"-C", repo, "-c", "commit.gpgsign=false"}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null", "HOME="+filepath.Join(filepath.Dir(repo), "home"))
	b, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v %s", args, err, b)
	}
	return string(b)
}
func TestPostCommitDetachedModesAndWaitExpiry(t *testing.T) {
	for _, mode := range []string{"daemon", "on-demand"} {
		t.Run(mode, func(t *testing.T) {
			config, repo := setup(t, mode)
			git(t, repo, "add", ".")
			receiptText := git(t, repo, "commit", "--quiet", "-m", "first")

			if !strings.Contains(receiptText, "run_id") || !strings.Contains(receiptText, "agent-guide") {
				t.Fatalf("missing durable receipt: %s", receiptText)
			}
			status, exit := invoke(t, config, repo, "status")
			if exit != 0 {
				t.Fatalf("status %+v", status)
			}
			b, _ := json.Marshal(status.Result)
			var page core.Page
			json.Unmarshal(b, &page)
			if len(page.Runs) != 1 {
				t.Fatalf("saved run missing: %s", b)
			}
			id := page.Runs[0].ID
			if page.Runs[0].State.Terminal() {
				t.Fatal("check completed before test released it")
			}
			if _, exit = invoke(t, config, repo, "wait", "--run", id, "--timeout", "1"); exit != 4 {
				t.Fatalf("wait exit %d", exit)
			}
			os.WriteFile(filepath.Join(repo, "source.txt"), []byte("dirty"), 0600)
			git(t, repo, "checkout", "-b", "another")
			os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600)
			if r, exit := invoke(t, config, repo, "wait", "--run", id, "--timeout", "15"); exit != 0 {
				t.Fatalf("wait %+v", r)
			}
			if r, exit := invoke(t, config, repo, "check"); exit != 0 {
				t.Fatalf("source/config isolation failed %+v", r)
			}
			if mode == "daemon" {
				if _, exit = invoke(t, config, repo, "self-update", "--version", "0.1.0"); exit != 2 {
					t.Fatalf("active update not refused: %d", exit)
				}
			}
		})
	}
}
func TestOfficialSDKStdioToolsAndNoDaemon(t *testing.T) {
	config, repo := setup(t, "on-demand")
	git(t, repo, "-c", "core.hooksPath=/dev/null", "add", ".")
	git(t, repo, "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "mcp")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	command := exec.Command(binary, "mcp", "--config", config)
	command.Env = append(os.Environ(), "HOME="+filepath.Join(filepath.Dir(config), "home"))
	client := mcp.NewClient(&mcp.Implementation{Name: "ach-integration", Version: "1"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != 13 {
		t.Fatalf("tools: %v %v", tools, err)
	}
	for _, name := range []string{"ach_plan", "ach_status", "ach_inbox", "ach_doctor", "ach_check"} {
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: map[string]any{"repo": repo}})
		if err != nil || result.IsError {
			t.Fatalf("%s: %v %v", name, result, err)
		}
	}
	paths := core.Paths{Config: config, Control: filepath.Join(filepath.Dir(config), "home", ".config", "async-commit-hook", "control")}
	s, err := core.Open(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	active, err := s.Active()
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range active {
		if c.Kind == "daemon" {
			t.Fatal("stdio queries started daemon")
		}
	}
	call := func(name string, args map[string]any) core.Output {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ach_" + name, Arguments: args})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		var out core.Output
		if err = json.Unmarshal(b, &out); err != nil || out.SchemaVersion != 1 {
			t.Fatalf("MCP response %s %v", b, err)
		}
		return out
	}
	decodeReceipt := func(out core.Output) core.Receipt {
		t.Helper()
		if out.Error != nil {
			t.Fatalf("MCP submission %+v", out.Error)
		}
		b, _ := json.Marshal(out.Result)
		var receipt core.Receipt
		json.Unmarshal(b, &receipt)
		if receipt.RunID == "" {
			t.Fatal("MCP receipt missing")
		}
		return receipt
	}
	receipt := decodeReceipt(call("run", map[string]any{"repo": repo}))
	expired := call("wait", map[string]any{"run_id": receipt.RunID, "timeout_seconds": 1})
	if expired.Error == nil || expired.Error.Code != "wait-expired" {
		t.Fatalf("MCP wait classification %+v", expired)
	}
	r, err := s.Store.Run(receipt.RunID)
	if err != nil || r.State.Terminal() {
		t.Fatal("MCP wait cancelled execution")
	}
	os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600)
	if result := call("wait", map[string]any{"run_id": receipt.RunID, "timeout_seconds": 10}); result.Error != nil {
		t.Fatal(result.Error)
	}
	for _, name := range []string{"logs", "failures", "compare", "ack"} {
		if result := call(name, map[string]any{"run_id": receipt.RunID, "check": "test"}); result.Error != nil {
			t.Fatalf("MCP %s %+v", name, result.Error)
		}
	}
	r, _ = s.Store.Run(receipt.RunID)
	if r.AcknowledgedAt == nil || !s.GateRun(r).Passed {
		t.Fatal("MCP acknowledgement/gate diverged")
	}
	rerun := decodeReceipt(call("rerun", map[string]any{"run_id": receipt.RunID}))
	if result := call("cancel", map[string]any{"run_id": rerun.RunID}); result.Error != nil {
		t.Fatal(result.Error)
	}
	call("wait", map[string]any{"run_id": rerun.RunID, "timeout_seconds": 10})
}

func TestConcurrentDaemonStartConverges(t *testing.T) {
	config, repo := setup(t, "daemon")
	type outcome struct {
		data []byte
		err  error
	}
	done := make(chan outcome, 6)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for i := 0; i < 6; i++ {
		go func() {
			cmd := exec.CommandContext(ctx, binary, "daemon", "start", "--config", config, "--json")
			cmd.Env = append(os.Environ(), "HOME="+filepath.Join(filepath.Dir(config), "home"))
			b, err := cmd.CombinedOutput()
			done <- outcome{b, err}
		}()
	}
	for i := 0; i < 6; i++ {
		r := <-done
		if r.err != nil {
			t.Fatalf("competing startup: %s %v", r.data, r.err)
		}
	}
	response, exit := invoke(t, config, repo, "daemon", "status")
	if exit != 0 {
		t.Fatalf("status %+v", response)
	}
	b, _ := json.Marshal(response.Result)
	var components []core.Component
	json.Unmarshal(b, &components)
	owners := 0
	for _, c := range components {
		if c.Kind == "daemon" {
			owners++
		}
	}
	if owners != 1 {
		t.Fatalf("got %d daemon owners: %s", owners, b)
	}
}

func TestTemporaryViewerExitLeavesWorkerAndHistory(t *testing.T) {
	config, repo := setup(t, "on-demand")
	git(t, repo, "add", ".")
	git(t, repo, "commit", "--quiet", "-m", "viewer")
	status, _ := invoke(t, config, repo, "status")
	b, _ := json.Marshal(status.Result)
	var page core.Page
	json.Unmarshal(b, &page)
	id := page.Runs[0].ID
	viewer := exec.Command(binary, "ui", "--config", config, "--run", id, "--json")
	viewer.Env = append(os.Environ(), "HOME="+filepath.Join(filepath.Dir(config), "home"))
	stdout, err := viewer.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = viewer.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = viewer.Process.Kill() })
	ready := make(chan string, 1)
	go func() {
		reader := bufio.NewScanner(stdout)
		if reader.Scan() {
			ready <- reader.Text()
		} else {
			ready <- ""
		}
	}()
	select {
	case line := <-ready:
		if !strings.Contains(line, "viewer-active") {
			t.Fatal("viewer did not report readiness")
		}
		var output core.Output
		if err := json.Unmarshal([]byte(line), &output); err != nil {
			t.Fatal(err)
		}
		verifyLocalUI(t, output.Result.(map[string]any)["url"].(string))
	case <-time.After(10 * time.Second):
		t.Fatal("viewer startup timed out")
	}
	viewer.Process.Signal(os.Interrupt)
	if err = viewer.Wait(); err != nil {
		t.Fatal(err)
	}
	status, exit := invoke(t, config, repo, "status", "--run", id)
	b, _ = json.Marshal(status.Result)
	var run core.Run
	json.Unmarshal(b, &run)
	if exit != 0 || run.State.Terminal() {
		t.Fatalf("viewer stopped check: %s", b)
	}
	os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600)
	if result, exit := invoke(t, config, repo, "wait", "--run", id, "--timeout", "15"); exit != 0 {
		t.Fatalf("worker did not finish: %+v", result)
	}
}

func TestDrainAndForcedStop(t *testing.T) {
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			config, repo := setup(t, "daemon")
			git(t, repo, "add", ".")
			git(t, repo, "commit", "--quiet", "-m", "stop")
			status, _ := invoke(t, config, repo, "status")
			b, _ := json.Marshal(status.Result)
			var page core.Page
			json.Unmarshal(b, &page)
			id := page.Runs[0].ID
			// Wait for process ownership before asking a normal stop to drain.
			deadline := time.Now().Add(10 * time.Second)
			for {
				status, _ = invoke(t, config, repo, "status", "--run", id)
				b, _ = json.Marshal(status.Result)
				var run core.Run
				json.Unmarshal(b, &run)
				if len(run.Checks) > 0 && run.Checks[0].State == core.Running {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("check did not start")
				}
				time.Sleep(20 * time.Millisecond)
			}
			args := []string{"daemon", "stop"}
			if force {
				args = append(args, "--force")
			}
			if result, exit := invoke(t, config, repo, args...); exit != 0 {
				t.Fatalf("stop %+v", result)
			}
			if !force {
				os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600)
			}
			result, exit := invoke(t, config, repo, "wait", "--run", id, "--timeout", "15")
			b, _ = json.Marshal(result.Result)
			var run core.Run
			json.Unmarshal(b, &run)
			want := core.Passed
			if force {
				want = core.Cancelled
			}
			if exit != 0 || run.State != want {
				t.Fatalf("stop force=%v result %s", force, b)
			}
		})
	}
}

func TestPrePushRunAndWaitCreatesMissingAttempt(t *testing.T) {
	config, repo := setup(t, "on-demand")
	git(t, repo, "-c", "core.hooksPath=/dev/null", "add", ".")
	git(t, repo, "-c", "core.hooksPath=/dev/null", "commit", "--quiet", "-m", "push")
	sha := strings.TrimSpace(git(t, repo, "rev-parse", "HEAD"))
	os.WriteFile(filepath.Join(filepath.Dir(config), "release-check"), nil, 0600)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "pre-push", "--policy", "run-and-wait", "--config", config, "--repo", repo, "--json")
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(filepath.Dir(config), "home"))
	cmd.Stdin = strings.NewReader(fmt.Sprintf("refs/heads/main %s refs/heads/main %s\n", sha, strings.Repeat("0", 40)))
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run-and-wait %v %s", err, b)
	}
	if result, exit := invoke(t, config, repo, "check", "--commit", sha); exit != 0 {
		t.Fatalf("push did not validate actual tip: %+v", result)
	}
}

func TestHomebrewOwnedExecutableRejectsSelfUpdate(t *testing.T) {
	config, _ := setup(t, "on-demand")
	cellar := filepath.Join(t.TempDir(), "Cellar", "async-commit-hook", "0.1.0", "bin")
	if err := os.MkdirAll(cellar, 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(cellar, "ach")
	if err = os.WriteFile(installed, data, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(installed, "self-update", "--version", "0.1.0", "--config", config, "--json")
	cmd.Env = append(os.Environ(), "HOME="+filepath.Join(filepath.Dir(config), "home"))
	b, err := cmd.CombinedOutput()
	if err == nil || !strings.Contains(string(b), "homebrew-owned") || !strings.Contains(string(b), "brew upgrade") {
		t.Fatalf("package ownership lost: %v %s", err, b)
	}
}
