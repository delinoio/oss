// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

func TestManualNativeSidechatSandboxSmoke(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native Sidechat with isolated scripted provider")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix command fixture; Windows native sandbox acceptance remains independent")
	}
	for _, action := range []string{"write", "network", "escalation", "permissions", "external-tool", "read"} {
		t.Run(action, func(t *testing.T) {
			var requests, externalRequests atomic.Int64
			var toolResult atomic.Bool
			external := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { externalRequests.Add(1); w.WriteHeader(204) }))
			defer external.Close()
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := requests.Add(1)
				body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
				var req struct {
					Tools []struct{ Name, Type string }
					Input []struct {
						Type   string
						CallID string `json:"call_id"`
						Output json.RawMessage
					}
				}
				if err != nil || r.Method != "POST" || r.URL.Path != "/responses" || n > 2 || json.Unmarshal(body, &req) != nil {
					t.Error("unexpected fixture request")
					http.Error(w, "invalid", 400)
					return
				}
				for _, tool := range req.Tools {
					if tool.Name == "request_permissions" || tool.Name == "spawn_agent" || strings.HasPrefix(tool.Name, "mcp__") || strings.Contains(tool.Name, "plugin") || tool.Name == "js" || tool.Type == "web_search" {
						t.Error("Sidechat exposed external/permission tool authority")
					}
				}
				if n == 2 {
					for _, item := range req.Input {
						if item.CallID != "call_sidechat_fixture" || item.Type != "function_call_output" {
							continue
						}
						var output string
						if json.Unmarshal(item.Output, &output) == nil && output != "" && (action != "read" || strings.Contains(output, "unchanged-read-only-fixture")) {
							toolResult.Store(true)
						}
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				send := func(event any) { raw, _ := json.Marshal(event); fmt.Fprintf(w, "data: %s\n\n", raw) }
				id := fmt.Sprintf("resp_sidechat_%d", n)
				send(map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress"}})
				var item any
				if n == 1 {
					name := "exec_command"
					args := map[string]any{"cmd": "printf changed > original.txt; printf created > sidechat-created.txt"}
					switch action {
					case "network":
						args["cmd"] = "/usr/bin/curl --max-time 2 " + external.URL
					case "escalation":
						args["sandbox_permissions"] = "require_escalated"
						args["justification"] = "Temporary denied write fixture"
					case "permissions":
						name = "request_permissions"
						args = map[string]any{"permissions": map[string]any{"file_system": map[string]any{"write": []string{"."}}, "network": map[string]any{"enabled": true}}}
					case "external-tool":
						name = "mcp__unconfigured__write"
						args = map[string]any{"text": "inert-fixture"}
					case "read":
						args["cmd"] = "cat original.txt"
					}
					raw, _ := json.Marshal(args)
					item = map[string]any{"type": "function_call", "id": "fc_sidechat_fixture", "call_id": "call_sidechat_fixture", "name": name, "arguments": string(raw)}
				} else {
					item = map[string]any{"type": "message", "id": "msg_sidechat_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Sidechat sandbox fixture complete."}}}
				}
				send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
				send(map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
			}))
			defer provider.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cfg := nativeFixtureConfig(t, binary, provider.URL)
			cfg.Sidechat = ReadOnlySidechatV1
			original := filepath.Join(cfg.Process.Cwd, "original.txt")
			if err := os.WriteFile(original, []byte("unchanged-read-only-fixture"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := Open(ctx, cfg)
			if err != nil {
				p := domain.SafeError(err)
				t.Fatalf("native profile: %s; %s", p.Code, p.Guidance)
			}
			defer client.Close()
			bound, err := client.StartThread(ctx, domain.NewID(), ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: string(ApprovalNever)}})
			if err != nil || bound.Effective == nil || !sidechatEffective(*bound.Effective) {
				t.Fatal("native read-only binding failed", err)
			}
			if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Run only the controlled read-only fixture."}); err != nil {
				t.Fatal(err)
			}
			for {
				event, err := client.NextEvent(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if event.Kind == InteractionRequestedEvent {
					t.Fatal("read-only Sidechat requested permission escalation")
				}
				if event.Kind == TurnCompletedEvent {
					if event.Turn == nil || event.Turn.Status != TurnCompleted || !event.Correlated {
						t.Fatal("native fixture not settled")
					}
					break
				}
			}
			if err := client.Close(); err != nil {
				t.Fatal(err)
			}
			contents, err := os.ReadFile(original)
			if err != nil || string(contents) != "unchanged-read-only-fixture" {
				t.Fatal("native Sidechat changed original workspace")
			}
			if _, err := os.Lstat(filepath.Join(cfg.Process.Cwd, "sidechat-created.txt")); !os.IsNotExist(err) {
				t.Fatal("native Sidechat created a file")
			}
			if requests.Load() != 2 || externalRequests.Load() != 0 || !toolResult.Load() {
				t.Fatal("native read/denial/network evidence incomplete")
			}
		})
	}
}

func TestManualNativeSidechatRejectsInheritedExternalTools(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native Sidechat inherited authority fixture")
	}
	if runtime.GOOS == "windows" {
		t.Skip("Unix inert command fixture")
	}
	for _, kind := range []string{"mcp", "notify", "hook", "plugin"} {
		t.Run(kind, func(t *testing.T) {
			cfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
			cfg.Sidechat = ReadOnlySidechatV1
			marker := filepath.Join(cfg.Process.Cwd, "external-authority-marker")
			text := ""
			switch kind {
			case "mcp":
				text = fmt.Sprintf("\n[mcp_servers.inherited]\ncommand=\"/usr/bin/touch\"\nargs=[%q]\n", marker)
			case "notify":
				text = fmt.Sprintf("\n[projects.%q]\ntrust_level=\"trusted\"\n", cfg.Process.Cwd)
			case "hook":
				text = "\n[[hooks.SessionStart]]\nmatcher=\"startup\"\n[[hooks.SessionStart.hooks]]\ntype=\"command\"\ncommand=\"false\"\n"
			case "plugin":
				text = "\n[plugins.\"inert@fixture\"]\nenabled=false\n"
			}
			// notify is replaced by a whole CLI array; it cannot be reintroduced by
			// project layering. Other tables merge and require explicit rejection.
			if kind == "notify" {
				text = fmt.Sprintf("notify=[\"/usr/bin/touch\",%q]\n", marker) + ""
			}
			path := filepath.Join(cfg.Home, "config.toml")
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if kind == "notify" {
				raw = append([]byte(text), raw...)
			} else {
				raw = append(raw, []byte(text)...)
			}
			if err := os.WriteFile(path, raw, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			client, err := Open(ctx, cfg)
			if kind == "notify" {
				if err != nil {
					t.Fatal("whole-array CLI suppression failed", err)
				}
				if err = client.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if client != nil {
					_ = client.Close()
				}
				assertCode(t, err, domain.Unsupported)
			}
			if _, err := os.Lstat(marker); !os.IsNotExist(err) {
				t.Fatal("inherited external authority ran before rejection")
			}
		})
	}
}
