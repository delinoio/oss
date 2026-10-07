package claude

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
)

func nativeCallerResponse(w http.ResponseWriter, sequence int64, stop NativeStopReason, blocks ...map[string]any) {
	w.Header().Set("Content-Type", "text/event-stream")
	emit := func(event map[string]any) {
		raw, _ := json.Marshal(event)
		_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
	}
	message := contentMessage(fmt.Sprintf("msg_programmatic_%d", sequence))
	message["model"] = "fixture-model"
	message["container"] = map[string]any{"id": "container_fixture", "expires_at": "2026-09-27T00:00:00Z", "skills": nil}
	emit(map[string]any{"type": "message_start", "message": message})
	for index, block := range blocks {
		start := map[string]any{}
		for key, value := range block {
			start[key] = value
		}
		var delta map[string]any
		if input, ok := block["input"]; ok {
			raw, _ := json.Marshal(input)
			start["input"] = map[string]any{}
			delta = map[string]any{"type": ToolInputDelta, "partial_json": string(raw)}
		} else if value, ok := block["text"]; ok {
			start["text"] = ""
			delta = map[string]any{"type": TextDelta, "text": value}
		}
		emit(map[string]any{"type": "content_block_start", "index": index, "content_block": start})
		if delta != nil {
			emit(map[string]any{"type": "content_block_delta", "index": index, "delta": delta})
		}
		emit(map[string]any{"type": "content_block_stop", "index": index})
	}
	emit(map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": stop, "stop_sequence": nil, "container": message["container"]}, "usage": map[string]any{"output_tokens": 9}})
	emit(map[string]any{"type": "message_stop"})
}

func TestManualNativeProgrammaticCallers(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit native binary and private scripted provider required")
	}
	for _, version := range []NativeCallerKind{CodeCaller20250825, CodeCaller20260120} {
		for _, mode := range []string{"server", "read", "bash", "pause"} {
			t.Run(fmt.Sprintf("%s/%s", version, mode), func(t *testing.T) {
				local := mode == "read" || mode == "bash"
				cfg, logs := apiFixtureConfig(t, "native-programmatic")
				cfg.Process.Executable, cfg.Model, cfg.Permission = binary, "fixture-model", DefaultPermission
				path := filepath.Join(cfg.Workspace, "native-programmatic.txt")
				if err := os.WriteFile(path, []byte("private native programmatic file\n"), 0600); err != nil {
					t.Fatal(err)
				}
				code, called, result, finished := programmaticFixtures(version)
				if local {
					called = map[string]any{"type": ToolUseBlock, "id": "toolu_programmatic_read", "name": "Read", "input": map[string]any{"file_path": path}, "caller": called["caller"]}
				}
				if mode == "bash" {
					called["name"], called["input"] = "Bash", map[string]any{"command": `if test -z "${ANTHROPIC_API_KEY-}" && test -z "${ANTHROPIC_AUTH_TOKEN-}"; then printf 'private native programmatic file'; else exit 7; fi`, "description": "Verify private programmatic tool credential isolation"}
				}
				var calls atomic.Int64
				provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.Method != http.MethodPost || r.URL.Path != "/provider/messages" || r.Header.Get("X-Api-Key") != nativeAPIUpstreamKey || r.Header.Get("Authorization") != "" {
						t.Error("programmatic fixture authority changed")
						w.WriteHeader(400)
						return
					}
					n := calls.Add(1)
					text := map[string]any{"type": TextBlock, "text": "Native programmatic answer."}
					if n == 1 {
						if mode == "pause" {
							nativeCallerResponse(w, n, "pause_turn", code)
						} else if local {
							nativeCallerResponse(w, n, "tool_use", code, called)
						} else {
							nativeCallerResponse(w, n, "end_turn", code, called, result, finished, text)
						}
						return
					}
					if n != 2 || mode == "server" {
						t.Error("unexpected programmatic request")
						w.WriteHeader(400)
						return
					}
					raw, err := io.ReadAll(io.LimitReader(r.Body, maxStreamFrame+1))
					if err != nil || len(raw) > maxStreamFrame {
						t.Error("invalid programmatic continuation body")
						w.WriteHeader(400)
						return
					}
					if local {
						read, ok := nativeFixtureToolResults(t, raw)["toolu_programmatic_read"]
						if !ok || read.Error || !nativeTaskTextContains(read.Content, "private native programmatic file") {
							t.Error("programmatic local tool lost its native result")
						}
					}
					var container struct {
						Container json.RawMessage `json:"container"`
					}
					if json.Unmarshal(raw, &container) != nil {
						t.Error("invalid native continuation request")
					}
					// This pinned native client drops the provider container. The
					// scripted response proves original event/callback ownership,
					// not a hosted programmatic execution round trip.
					if len(container.Container) != 0 {
						t.Error("pinned native container omission changed; review hosted continuation evidence")
					}
					nativeCallerResponse(w, n, "end_turn", finished, text)
				}))
				defer provider.Close()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				authority := nativeAPIAuthority{ctx: ctx, scope: apiproxy.Scope{ExecutionID: domain.NewID(), SessionID: cfg.SessionID, AccountID: domain.NewID(), ConnectionID: domain.NewID(), ProviderID: domain.NewID(), ModelID: domain.NewID(), NativeModel: cfg.Model, Provider: domain.Provider{Name: "Programmatic fixture", Endpoint: provider.URL + "/provider", Protocol: domain.AnthropicMessages, Authentication: domain.APIKeyAuth, Enabled: new(true)}, Operations: []apiproxy.Operation{apiproxy.MessageCreate}}}
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
					for _, private := range []string{nativeAPIFixtureToken, nativeAPIUpstreamKey, "private native", "private provider", path} {
						if strings.Contains(logs.String(), private) {
							t.Error("private programmatic content entered logs")
						}
					}
				}()
				if _, err := s.SendInput(ctx, domain.NewID(), "Observe the original programmatic call.", ContinueSuccessfulRun); err != nil {
					t.Fatal(err)
				}
				var idle, success, approved, echoed bool
				for !idle {
					event, err := s.Next(ctx)
					if err != nil {
						t.Fatal("native programmatic lifecycle failed", err, logs.String())
					}
					if event.Kind == InputFinished {
						success = event.Result.Successful()
					}
					if event.Kind == UncorrelatedTermination && mode == "pause" {
						// 2.1.236 does not perform another provider request for this
						// pause-only response. Preserve its uncorrelated failure and
						// open operation instead of fabricating completion/retry.
						if event.Result == nil || !event.Result.Error || calls.Load() != 1 || s.current.problem == nil || s.current.content.openTools != 1 || s.current.content.serverTools["code_original"].finished {
							t.Fatal("native pause failure lost original pending work")
						}
						if _, err := s.SendInput(ctx, domain.NewID(), "Must remain blocked.", ResumeTerminalRun); err == nil {
							t.Fatal("native pause failure authorized another input")
						}
						return
					}
					if event.Interaction != nil && event.Interaction.Kind == InteractionRequested {
						request := event.Interaction.Request
						if mode != "bash" || approved || request.Kind != ToolPermission || request.ToolID != "toolu_programmatic_read" || request.CalledBy != (NativeToolCaller{Kind: version, ToolID: "code_original"}) {
							t.Fatal("programmatic call changed original approval scope")
						}
						if err := s.Reply(ctx, event.Interaction.ArrivalID, PermissionReply{Behavior: PermissionAllow}); err != nil {
							t.Fatal(err)
						}
						approved = true
					}
					if event.Interaction != nil && event.Interaction.Kind == InteractionReplyEchoed {
						echoed = true
					}
					idle = event.Kind == RunStateObserved && event.Run.State == RunIdle
				}
				wantCalls := int64(1)
				var actual NativeToolCaller
				if local {
					wantCalls = 2
					actual = s.current.content.tools["toolu_programmatic_read"].caller
				} else if mode == "server" {
					actual = s.current.content.serverTools["server_fixture"].caller
				}
				wantCaller := NativeToolCaller{Kind: version, ToolID: "code_original"}
				if mode == "pause" {
					wantCalls, wantCaller = 2, NativeToolCaller{}
				}
				if !success || calls.Load() != wantCalls || s.current.content.openTools != 0 || !s.current.content.serverTools["code_original"].finished || actual != wantCaller || (mode == "bash" && (!approved || !echoed)) {
					t.Fatal("native programmatic ancestry or completion changed")
				}
			})
		}
	}
}
