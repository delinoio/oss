// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This installed-native fixture keeps both parent and child on the same mocked
// API route/model. It carries no provider credential or real account acceptance.
func TestManualNativeOpenRouterSubagentLifecycle(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex opt-in required")
	}
	version, err := exec.Command(binary, "--version").Output()
	if err != nil {
		t.Fatal(err)
	}
	var parents, children atomic.Int32

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api-proxy/v1/responses" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken() {
			t.Error("subagent changed original route")
			http.Error(w, "unsupported", 404)
			return
		}
		raw, _ := io.ReadAll(r.Body)
		var request struct {
			Model string `json:"model"`
			Input []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"input"`
		}
		if json.Unmarshal(raw, &request) != nil || request.Model != "openai/gpt-6-luna" {
			t.Error("subagent changed original model")
		}
		// fork_turns=none retains the parent's model and route but does not
		// copy its user prompt. The child task arrives as developer content.
		child := true
		for _, input := range request.Input {
			if input.Role == "user" && strings.Contains(string(input.Content), "너가 쓸 수 있는 툴들 다 한번씩 써봐") {
				child = false
			}
		}

		var item any
		if child {
			children.Add(1)
			item = map[string]any{"type": "message", "id": "msg_child", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Child fixture complete."}}}
		} else {
			n := parents.Add(1)
			switch n {
			case 1:
				item = map[string]any{"type": "function_call", "id": "fc_spawn", "call_id": "call_spawn", "name": "spawn_agent", "namespace": "collaboration", "arguments": `{"task_name":"fixture_child","fork_turns":"none","message":"DELIDEV_CHILD_FIXTURE: return only fixture complete."}`}
			case 2:
				item = map[string]any{"type": "function_call", "id": "fc_list", "call_id": "call_list", "name": "list_agents", "namespace": "collaboration", "arguments": `{}`}
			default:
				item = map[string]any{"type": "message", "id": "msg_parent", "role": "assistant", "content": []any{map[string]string{"type": "output_text", "text": "Parent fixture complete."}}}
			}
		}
		id := fmt.Sprintf("resp_%d_%v", parents.Load(), child)
		w.Header().Set("Content-Type", "text/event-stream")
		for _, frame := range []any{map[string]any{"type": "response.created", "response": map[string]any{"id": id, "status": "in_progress"}}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": map[string]any{"id": id, "status": "completed", "output": []any{}, "usage": map[string]int{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}} {
			data, _ := json.Marshal(frame)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", data)
		}
	}))
	defer provider.Close()
	cfg := nativeFixtureConfig(t, binary, provider.URL)
	cfg.Version = strings.TrimPrefix(strings.TrimSpace(string(version)), "codex-cli ")
	file, err := os.OpenFile(filepath.Join(cfg.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString("\n[features]\nmulti_agent_v2 = true\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	cfg.API = &APIConfig{OpenRouterTools: true, ServerOrigin: provider.URL, Token: apiFixtureToken()}
	cfg.DynamicTools = true
	cfg.Process.Logger = slog.New(slog.NewTextHandler(os.Stderr, nil))
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	client, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	settings := ThreadSettings{Model: "openai/gpt-6-luna", Provider: APIProvider, Effort: "xhigh", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionFullAccess, ApprovalPolicy: "on-request", ApprovalsReviewer: domain.CodexReviewerAuto}}
	if _, err := client.StartThread(ctx, domain.NewID(), settings); err != nil {
		t.Fatal(err)
	}
	if _, err := client.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "너가 쓸 수 있는 툴들 다 한번씩 써봐"}); err != nil {
		t.Fatal(err)
	}
	rootDone := false
	for {
		event, err := client.NextEvent(ctx)
		if err != nil {
			t.Fatalf("subagent native lifecycle failed: %v parent=%d child=%d", err, parents.Load(), children.Load())
		}
		if event.Kind == SubagentActivityEvent {
			if _, err := client.InspectDescendants(ctx); err != nil {
				t.Fatal(err)
			}
		}
		if event.Kind == TurnCompletedEvent {
			rootDone = true
		}
		if rootDone && children.Load() > 0 {
			snapshot, err := client.InspectDescendants(ctx)
			if err != nil {
				t.Fatal(err)
			}
			complete := len(snapshot.Subagents) > 0
			for _, child := range snapshot.Subagents {
				complete = complete && child.Status.Terminal()
			}
			if complete {
				break
			}
		}
	}
	if parents.Load() != 3 || children.Load() != 1 {
		t.Fatalf("native subagent did not execute original route: parent=%d child=%d", parents.Load(), children.Load())
	}
}
