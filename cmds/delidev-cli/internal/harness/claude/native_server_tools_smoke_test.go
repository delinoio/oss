package claude

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func TestManualNativeServerWebTools(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	for _, name := range []ServerToolName{ServerWebSearch, ServerWebFetch} {
		for _, child := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/child_%t", name, child), func(t *testing.T) {
				cfg, logs := apiFixtureConfig(t, "native-server-tools")
				cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
				var calls atomic.Int64
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
						t.Error("native server tool fixture changed provider authority/count")
						w.WriteHeader(400)
						return
					}
					sequence := calls.Add(1)
					if child && sequence == 1 {
						nativeFixtureToolResponse(w, sequence, "Agent", "toolu_server_parent", map[string]any{"description": "Inspect native server results", "prompt": "Return the native server tool answer.", "subagent_type": "general-purpose", "run_in_background": false})
						return
					}
					if child && sequence == 3 {
						raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
						if err != nil || len(raw) > maxStreamFrame {
							t.Error("native server child result exceeded its bound")
							w.WriteHeader(400)
							return
						}
						result, ok := nativeFixtureToolResults(t, raw)["toolu_server_parent"]
						if !ok || result.Error || !nativeTaskTextContains(result.Content, "Native server tool answer.") {
							t.Error("native parent lost original child result")
						}
						nativeFixtureTextResponse(w, sequence)
						return
					}
					if (!child && sequence != 1) || (child && sequence != 2) {
						t.Error("native server tool fixture requested an extra provider turn")
						w.WriteHeader(400)
						return
					}
					input := json.RawMessage(`{"query":"Private native server query."}`)
					kind := WebSearchResultBlock
					var result any = []any{map[string]any{"type": "web_search_result", "url": "https://fixture.invalid/source", "title": "Private native source title", "page_age": nil, "encrypted_content": "private-server-search-data"}}
					if name == ServerWebFetch {
						input = json.RawMessage(`{"url":"https://fixture.invalid/source"}`)
						kind = WebFetchResultBlock
						result = map[string]any{"type": "web_fetch_result", "url": "https://fixture.invalid/source", "retrieved_at": "2026-09-26T00:00:00Z", "content": map[string]any{"type": "document", "title": "Private native source title", "citations": map[string]any{"enabled": true}, "source": map[string]any{"type": "text", "media_type": "text/plain", "data": "Private native fetched text."}}}
					}
					w.Header().Set("Content-Type", "text/event-stream")
					message := contentMessage("msg_server_fixture")
					message["model"] = cfg.Model
					for _, event := range []map[string]any{
						{"type": "message_start", "message": message},
						{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "server_tool_use", "id": "srvtoolu_fixture", "name": name, "input": map[string]any{}}},
						{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
						{"type": "content_block_stop", "index": 0},
						{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": kind, "tool_use_id": "srvtoolu_fixture", "content": result}},
						{"type": "content_block_stop", "index": 1},
						{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "text", "text": ""}},
						{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "text_delta", "text": "Native server tool answer."}},
						{"type": "content_block_stop", "index": 2},
						{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 7}},
						{"type": "message_stop"},
					} {
						raw, _ := json.Marshal(event)
						_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
					}
				}))
				defer provider.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Server tool fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth, Enabled: new(true)}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
				relay := httptest.NewServer(apiproxy.New(authority, slog.New(slog.NewJSONHandler(io.Discard, nil))))
				defer relay.Close()
				cfg.API.ServerOrigin = relay.URL
				s, err := OpenAPISession(ctx, cfg)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := s.Close(); err != nil {
						t.Error(err)
					}
					if err := process.ReconcileOwner(cfg.Process.Directory, cfg.Process.OwnerID); err != nil {
						t.Error(err)
					}
					for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "Private native", "private-server-search-data"} {
						if strings.Contains(logs.String(), private) {
							t.Error("private native server content entered logs")
						}
					}
				}()
				if _, err := s.SendInput(ctx, domain.NewID(), "Observe original server tool blocks.", ContinueSuccessfulRun); err != nil {
					t.Fatal(err)
				}
				bindings := map[string]ChildHistoryBinding{}
				var started, returned, succeeded, idle bool
				for !idle {
					observation, err := s.Next(ctx)
					if err != nil {
						t.Fatal("native server tool lifecycle failed", err, logs.String())
					}
					if task := observation.Task; task != nil && task.Kind == TaskStarted && task.ToolID != nil && task.SubagentType != nil && task.Description != nil && task.SpawnDepth != nil {
						bindings[task.ID] = ChildHistoryBinding{TaskID: task.ID, ToolID: *task.ToolID, AgentType: *task.SubagentType, Description: *task.Description, SpawnDepth: *task.SpawnDepth}
					}
					for _, content := range observation.Content {
						blocks := content.Blocks
						if content.Kind == ContentCompleted && content.Block != nil {
							blocks = []NativeContentBlock{*content.Block}
						} else if content.Kind != ProviderMessageSnapshot {
							continue
						}
						for _, block := range blocks {
							if tool := block.ServerTool; tool != nil {
								started = tool.ID == "srvtoolu_fixture" && tool.Name == name
							}
							if result := block.ServerResult; result != nil {
								returned = result.ID == "srvtoolu_fixture" && result.Name == name && result.Problem == ""
								if name == ServerWebSearch {
									returned = returned && len(result.Search) == 1 && result.Search[0].URL == "https://fixture.invalid/source"
								} else {
									returned = returned && result.Document != nil && result.Document.Source.Data != nil && *result.Document.Source.Data == "Private native fetched text."
								}
							}
						}
						published, _ := json.Marshal(content)
						if bytes.Contains(published, []byte("private-server-search-data")) || bytes.Contains(published, []byte("Private native fetched text.")) {
							t.Fatal("server result bypassed private content publication")
						}
					}
					if observation.Kind == InputFinished {
						succeeded = observation.Result.Successful()
					}
					idle = observation.Kind == RunStateObserved && observation.Run.State == RunIdle
				}
				wantCalls, wantLocal, parent := int64(1), 0, ""
				if child {
					wantCalls, wantLocal, parent = 3, 1, "toolu_server_parent"
				}
				if !succeeded || calls.Load() != wantCalls || len(s.current.content.tools) != wantLocal || s.current.content.openTools != 0 {
					t.Fatal("native server tool acquired local ownership or lost original completion")
				}
				if !child {
					if !started || !returned || !s.current.content.serverTools["srvtoolu_fixture"].finished || s.current.content.serverTools["srvtoolu_fixture"].parent != parent {
						t.Fatal("native root server observation lost ownership")
					}
				} else {
					// The pinned native runtime does not forward this child's final
					// provider-only blocks. Verify original files separately instead
					// of manufacturing live server-tool events from the parent text.
					if started || returned || len(s.current.content.serverTools) != 0 {
						t.Fatal("unforwarded child server content became synthetic live events")
					}
					if err := s.Close(); err != nil {
						t.Fatal(err)
					}
					verified := 0
					for id, task := range s.current.tasks {
						if task.kind != LocalAgentTask || task.tool != parent || !task.notified || !task.status.terminal() {
							t.Fatal("native server child task ownership changed")
						}
						raw, metadata := nativeFixtureChildFiles(t, cfg, id)
						if _, err := VerifyChildTranscript(ctx, raw, metadata, cfg.SessionID, cfg.Workspace, bindings[id], nil); err != nil {
							t.Fatal(err)
						}
						var storedUse, storedResult bool
						for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte{'\n'}) {
							var record struct {
								Type    string `json:"type"`
								Message struct {
									Content []json.RawMessage `json:"content"`
								} `json:"message"`
							}
							if json.Unmarshal(line, &record) != nil || record.Type != "assistant" {
								continue
							}
							for _, content := range record.Message.Content {
								block, err := decodeContentBlock(content)
								if err != nil {
									t.Fatal(err)
								}
								if block.ServerTool != nil {
									storedUse = block.ServerTool.ID == "srvtoolu_fixture" && block.ServerTool.Name == name
								}
								if block.ServerResult != nil {
									storedResult = block.ServerResult.ID == "srvtoolu_fixture" && block.ServerResult.Name == name
								}
							}
						}
						if !storedUse || !storedResult {
							t.Fatal("native child file lost original server operations")
						}
						verified++
					}
					if verified != 1 {
						t.Fatal("native server child history count changed")
					}
				}
			})
		}
	}
}
