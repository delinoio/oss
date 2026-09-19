//go:build !windows

package integration

import (
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
				if _, exit = invoke(t, config, repo, "self-update", "--version", "1.0.0"); exit != 2 {
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
}
