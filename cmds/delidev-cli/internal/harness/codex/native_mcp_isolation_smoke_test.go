// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// TestManualNativeOrdinaryExecutionNeverLaunchesUnselectedMCP drives the
// installed Codex through the API execution profile in a Git repository whose
// own or private-home configuration defines an MCP server that runs a marker
// command. No DeliDev MCP selection exists, so the server must never launch.
func TestManualNativeOrdinaryExecutionNeverLaunchesUnselectedMCP(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed harness with a private scripted loopback provider only")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix marker command fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required for repository trust resolution")
	}
	scenarios := []string{"repository-layer", "private-home-layer", "retained-trusted-home", "resume", "selected-profile"}
	scenarios = append(scenarios, "plugin")
	for _, scenario := range scenarios {
		t.Run(scenario, func(t *testing.T) {
			var requests atomic.Int64
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.URL.Path != apiproxy.Prefix+"/responses" {
					http.Error(w, "unsupported", http.StatusForbidden)
					return
				}
				w.Header().Set("Content-Type", "text/event-stream")
				for _, event := range []any{
					map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_mcp_fixture", "status": "in_progress"}},
					map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_mcp_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Isolation fixture complete."}}}},
					map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_mcp_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
				} {
					raw, _ := json.Marshal(event)
					_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
				}
			}))
			defer provider.Close()
			cfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
			cfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken()}
			workspace := cfg.Process.Cwd
			if output, err := exec.Command("git", "init", "-q", workspace).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v %s", err, output)
			}
			marker := filepath.Join(filepath.Dir(cfg.Home), "mcp-marker")
			server := fmt.Sprintf("[mcp_servers.marker]\ncommand = \"/usr/bin/touch\"\nargs = [%q]\n", marker)
			switch scenario {
			case "private-home-layer":
				appendNativeConfig(t, filepath.Join(cfg.Home, "config.toml"), "\n"+server)
			case "selected-profile":
				appendNativeConfig(t, filepath.Join(cfg.Home, "config.toml"), "\nprofile = \"review\"\n"+strings.Replace(server, "[mcp_servers.marker]", "[profiles.review.mcp_servers.marker]", 1))
			case "plugin":
				writeNativeMarkerPlugin(t, cfg.Home, marker)
			case "retained-trusted-home":
				// An earlier thread of the retained home recorded trust.
				appendNativeConfig(t, filepath.Join(cfg.Home, "config.toml"), fmt.Sprintf("\n[projects.%q]\ntrust_level = \"trusted\"\n", workspace))
				fallthrough
			default:
				if err := os.MkdirAll(filepath.Join(workspace, ".codex"), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(workspace, ".codex", "config.toml"), []byte(server), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			client, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			settings := ThreadSettings{Model: "fixture-model", Provider: APIProvider, Effort: "high", Cwd: workspace, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: "on-request"}}
			var bound ThreadResult
			if scenario == "resume" {
				// Complete one turn, close the connection and resume the retained
				// thread on a new one: the resume binding is isolated as well.
				first, err := client.StartThread(ctx, domain.NewID(), settings)
				if err != nil {
					t.Fatal(err)
				}
				runNativeIsolationTurn(t, ctx, client)
				if err := client.Close(); err != nil {
					t.Fatal(err)
				}
				cfg.Process.OwnerID = domain.NewID()
				if client, err = Open(ctx, cfg); err != nil {
					t.Fatal(err)
				}
				defer client.Close()
				if bound, err = client.ResumeThread(ctx, domain.NewID(), first.Thread.ID, settings); err != nil {
					t.Fatal(err)
				}
			} else if bound, err = client.StartThread(ctx, domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			if bound.Effective == nil || bound.Effective.Sandbox.Type != WorkspaceWrite || bound.Effective.ApprovalPolicy != ApprovalOnRequest {
				t.Fatalf("selected sandbox or approval changed: %+v", bound.Effective)
			}
			if err := client.verifyMCPInventory(ctx, workspace); err != nil {
				t.Fatalf("effective MCP inventory is not empty: %v", err)
			}
			if scenario != "resume" {
				runNativeIsolationTurn(t, ctx, client)
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatal("the unselected MCP server command ran")
			}
			if requests.Load() < 1 {
				t.Fatal("the scripted provider was not used")
			}
			t.Logf("installed Codex %s: %s binding kept sandbox and approval, empty effective MCP inventory, marker absent; scripted loopback provider only, no real account", client.Version(), scenario)
		})
	}
}

func appendNativeConfig(t *testing.T, path, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

// runNativeIsolationTurn sends exactly one input and waits for its completion.
func runNativeIsolationTurn(t *testing.T, ctx context.Context, client *Client) {
	t.Helper()
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Return the scripted local fixture response only."}); err != nil {
		t.Fatal(err)
	}
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if event.Native != nil && event.Native.Method == "mcpServer/startupStatus/updated" {
			t.Fatal("an unselected MCP server reported startup")
		}
		if event.Kind == TurnCompletedEvent {
			if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
				t.Fatal("scripted turn did not complete")
			}
			return
		}
	}
}

// TestManualNativeForkNeverLaunchesUnselectedMCP covers the independent Fork
// route: the child runtime binds a workspace-write thread inside a repository
// that defines an MCP server, and the server must not launch.
func TestManualNativeForkNeverLaunchesUnselectedMCP(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed harness with a private scripted loopback provider only")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix marker command fixture")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("Git is required for repository trust resolution")
	}
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_fork_mcp", "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_fork_mcp", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Fork isolation fixture complete."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fork_mcp", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer provider.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	sourceCfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
	sourceCfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken()}
	settings := ThreadSettings{Model: "fixture-model", Provider: APIProvider, Effort: "high", Cwd: sourceCfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionWorkspaceWrite, ApprovalPolicy: "on-request"}}
	client, err := Open(ctx, sourceCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	bound, err := client.StartThread(ctx, domain.NewID(), settings)
	if err != nil {
		t.Fatal(err)
	}
	input := domain.NewID()
	turn, err := client.StartTurn(ctx, domain.NewID(), input, domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Original fork input"})
	if err != nil {
		t.Fatal(err)
	}
	finishNativeForkTurn(t, ctx, client)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	sourceCfg.Process.OwnerID = domain.NewID()
	reader, err := Open(ctx, sourceCfg)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := ContinuationCheckpoint{ThreadID: bound.Thread.ID, SessionID: bound.Thread.SessionID, TurnID: turn.TurnID, Status: TurnCompleted, Mode: domain.ExecuteMode, Effective: *bound.Effective, Inputs: []HistoricalInput{{ID: input, PromptDigest: sha256.Sum256([]byte("Original fork input"))}}}
	source, inspectErr := reader.InspectForkSource(ctx, checkpoint)
	if closeErr := reader.Close(); inspectErr != nil || closeErr != nil {
		t.Fatalf("source inspection: %v; cleanup: %v", inspectErr, closeErr)
	}
	childCfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
	childCfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken()}
	workspace := childCfg.Process.Cwd
	if output, err := exec.Command("git", "init", "-q", workspace).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v %s", err, output)
	}
	marker := filepath.Join(filepath.Dir(childCfg.Home), "mcp-marker")
	if err := os.MkdirAll(filepath.Join(workspace, ".codex"), 0o700); err != nil {
		t.Fatal(err)
	}
	server := fmt.Sprintf("[mcp_servers.marker]\ncommand = \"/usr/bin/touch\"\nargs = [%q]\n", marker)
	if err := os.WriteFile(filepath.Join(workspace, ".codex", "config.toml"), []byte(server), 0o600); err != nil {
		t.Fatal(err)
	}
	// The child's retained home already records the repository as trusted.
	appendNativeConfig(t, filepath.Join(childCfg.Home, "config.toml"), fmt.Sprintf("\n[projects.%q]\ntrust_level = \"trusted\"\n", workspace))
	child, err := Open(ctx, childCfg)
	if err != nil {
		t.Fatal(err)
	}
	defer child.Close()
	settings.Cwd = workspace
	fork, err := child.ForkThread(ctx, domain.NewID(), source, settings)
	if err != nil {
		problem := domain.SafeError(err)
		t.Fatalf("native fork: %s; %s", problem.Code, problem.Guidance)
	}
	if fork.Effective == nil || fork.Effective.Sandbox.Type != WorkspaceWrite || fork.Effective.ApprovalPolicy != ApprovalOnRequest {
		t.Fatalf("selected sandbox or approval changed: %+v", fork.Effective)
	}
	if err := child.verifyMCPInventory(ctx, workspace); err != nil {
		t.Fatalf("effective MCP inventory is not empty: %v", err)
	}
	if err := child.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(marker); !os.IsNotExist(err) {
		t.Fatal("the unselected MCP server command ran")
	}
}

// writeNativeMarkerPlugin installs an enabled plugin whose own .mcp.json
// defines a server that runs the marker command.
func writeNativeMarkerPlugin(t *testing.T, home, marker string) {
	t.Helper()
	root := filepath.Join(home, "plugins", "cache", "fixture-market", "marker-plugin", "1.0.0")
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o700); err != nil {
		t.Fatal(err)
	}
	manifest := `{"name":"marker-plugin","version":"1.0.0","description":"fixture"}`
	servers := fmt.Sprintf(`{"mcpServers":{"plugin-marker":{"command":"/usr/bin/touch","args":[%q]}}}`, marker)
	for name, content := range map[string]string{filepath.Join(".codex-plugin", "plugin.json"): manifest, ".mcp.json": servers} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	appendNativeConfig(t, filepath.Join(home, "config.toml"), "\n[plugins.\"marker-plugin@fixture-market\"]\nenabled = true\n")
}
