// SPDX-License-Identifier: Apache-2.0
package codex

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestManualNativeCodexSubagentDefaultsAndConcurrency(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary with isolated scripted parent/child provider only")
	}
	var roots, children atomic.Int32
	childRelease := make(chan struct{})
	var rootModel, childModel string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != apiproxy.Prefix+"/responses" || r.Header.Get("Authorization") != "Bearer "+apiFixtureToken() {
			t.Error("child changed the original account route")
			http.Error(w, "denied", 403)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if err != nil {
			t.Error(err)
			return
		}
		var request struct {
			Model     string `json:"model"`
			Reasoning struct {
				Effort string `json:"effort"`
			} `json:"reasoning"`
			Tools []struct {
				Name  string `json:"name"`
				Tools []struct {
					Name string `json:"name"`
				} `json:"tools"`
			} `json:"tools"`
		}
		if json.Unmarshal(raw, &request) != nil {
			t.Error("invalid native request")
			return
		}
		responseID := "resp_subagent_child"
		var item any
		switch request.Model {
		case childModel:
			children.Add(1)
			select {
			case <-childRelease:
			case <-r.Context().Done():
				return
			}
			if request.Reasoning.Effort != "medium" {
				t.Error("child default effort was not applied")
			}
			item = map[string]any{"type": "message", "id": "msg_child", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Child fixture finished."}}}
		case rootModel:
			n := roots.Add(1)
			responseID = fmt.Sprintf("resp_subagent_root_%d", n)
			if request.Reasoning.Effort != "high" {
				t.Error("child configuration replaced root effort")
			}
			if n <= 2 {
				namespace := ""
				found := false
				for _, tool := range request.Tools {
					if tool.Name == "spawn_agent" {
						found = true
					}
					for _, nested := range tool.Tools {
						if nested.Name == "spawn_agent" {
							found = true
							namespace = tool.Name
						}
					}
				}
				if !found {
					var keys map[string]json.RawMessage
					_ = json.Unmarshal(raw, &keys)
					names := []string{}
					for key := range keys {
						names = append(names, key)
					}
					for _, tool := range request.Tools {
						names = append(names, "tool:"+tool.Name)
					}
					var entries []map[string]json.RawMessage
					_ = json.Unmarshal(keys["tools"], &entries)
					for _, entry := range entries {
						var kind, namespace string
						_ = json.Unmarshal(entry["type"], &kind)
						_ = json.Unmarshal(entry["namespace"], &namespace)
						names = append(names, "kind:"+kind+":"+namespace)
					}
					t.Errorf("native spawn tool unavailable: model=%s tool count=%d request fields=%v", request.Model, len(request.Tools), names)
				}
				item = map[string]any{"type": "function_call", "id": fmt.Sprintf("fc_spawn_%d", n), "call_id": fmt.Sprintf("call_spawn_%d", n), "name": "spawn_agent", "namespace": namespace, "arguments": fmt.Sprintf(`{"task_name":"fixture_%d","fork_turns":"none","message":"Return the controlled child response only."}`, n)}
			} else {
				close(childRelease)
				item = map[string]any{"type": "message", "id": "msg_root", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Root fixture finished."}}}
			}
		default:
			t.Error("native request escaped the explicit model set")
			http.Error(w, "denied", 403)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}}, map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item}, map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}}} {
			raw, _ := json.Marshal(event)
			fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer provider.Close()
	cfg := nativeFixtureConfig(t, binary, "http://127.0.0.1:1")
	// Exercise the pinned native V2 collaboration profile. The new model
	// presets require Code Mode and its separate runtime, outside this fixture.
	configFile, err := os.OpenFile(filepath.Join(cfg.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	_, err = configFile.WriteString("\n[features]\nmulti_agent_v2 = true\n")
	if closeErr := configFile.Close(); err != nil || closeErr != nil {
		t.Fatal("private native profile failed")
	}
	cfg.API = &APIConfig{ServerOrigin: provider.URL, Token: apiFixtureToken()}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	c, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	models, err := c.readModelList(ctx, true)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range models {
		high, medium := false, false
		for _, effort := range model.Reasoning {
			high = high || effort == "high"
			medium = medium || effort == "medium"
		}
		if high && medium {
			if model.Model == "gpt-5.5" {
				rootModel = model.Model
			}
			if model.Model == "gpt-5.4" {
				childModel = model.Model
			}
		}
	}
	if rootModel == "" || childModel == "" {
		t.Fatal("pinned native catalog lacks two compatible scripted models")
	}
	s := ThreadSettings{Model: rootModel, Provider: APIProvider, Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request", SubagentModel: childModel, SubagentEffort: "medium", MaxConcurrency: 1}}
	root, err := c.StartThread(ctx, domain.NewID(), s)
	if err != nil || root.Effective.Model != rootModel {
		t.Fatal("native parent settings failed", err)
	}
	if _, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use the controlled provider script only."}); err != nil {
		t.Fatal(err)
	}
	for {
		e, err := c.NextEvent(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind == TurnCompletedEvent {
			if e.Turn == nil || e.Turn.Status != TurnCompleted {
				t.Fatal("parent did not settle")
			}
			break
		}
	}
	if roots.Load() != 3 || children.Load() != 1 {
		t.Fatalf("native default/concurrency profile did not run exactly one child: root=%d child=%d", roots.Load(), children.Load())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	t.Logf("Codex %s applied original child model/medium effort and child concurrency 1 with independent root/high settings; controlled provider and one execution credential, no real account", SupportedVersion)
}
