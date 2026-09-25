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
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// This opt-in profile invokes only a private shell builtin or private file
// patch supplied by the local scripted provider. No installed user account or
// external inference endpoint participates in this approval evidence.
func TestManualNativeApprovalResponse(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed native harness with local scripted responses only")
	}
	for _, kind := range []string{"command-accept", "command-cancel", "file-accept", "permissions-grant", "permissions-session", "permissions-strict", "permissions-empty"} {
		t.Run(kind, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			provider, requests, output := nativeApprovalProvider(t, kind)
			cfg := nativeFixtureConfig(t, binary, provider.URL)
			cfg.Process.Logger = slog.New(slog.NewJSONHandler(os.Stderr, nil))
			if strings.HasPrefix(kind, "permissions-") {
				// This under-development native tool is opt-in only in this private
				// fixture, matching the pinned upstream app-server test profile.
				file, err := os.OpenFile(filepath.Join(cfg.Home, "config.toml"), os.O_APPEND|os.O_WRONLY, 0o600)
				if err != nil {
					t.Fatal(err)
				}
				_, err = file.WriteString("\n[features]\nrequest_permissions_tool = true\n")
				_ = file.Close()
				if err != nil {
					t.Fatal(err)
				}
			}
			c, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(); err != nil {
					t.Error(err)
				}
			})
			settings := ThreadSettings{Model: "fixture-model", Provider: "delidev_fixture", Effort: "high", Cwd: cfg.Process.Cwd, Options: domain.AgentOptions{Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}}
			if _, err := c.StartThread(ctx, domain.NewID(), settings); err != nil {
				t.Fatal(err)
			}
			turn, err := c.StartTurn(ctx, domain.NewID(), domain.NewID(), domain.SessionInput{Mode: domain.ExecuteMode, Prompt: "Use only the scripted private approval fixture."})
			if err != nil {
				t.Fatal(err)
			}
			var arrival domain.ID
			closed, toolCompleted, accepted := false, false, false
			expectAccepted := strings.HasPrefix(kind, "permissions-") || kind == "command-accept" || kind == "file-accept"
			for {
				event, err := c.NextEvent(ctx)
				if err != nil {
					t.Fatalf("native approval event after request=%t closure=%t: %v", arrival != "", closed, err)
				}
				if event.Kind == InteractionRequestedEvent {
					if arrival != "" || event.TurnID != turn.TurnID || !event.Correlated || event.Interaction.Kind != ApprovalInteraction || event.Interaction.Approval == nil {
						t.Fatal("native approval lost original ownership")
					}
					request := event.Interaction.Approval
					if strings.HasPrefix(kind, "permissions-") {
						if request.Kind != PermissionsApproval || request.Permissions == nil || request.Permissions.Cwd != cfg.Process.Cwd || request.Permissions.Permissions.FileSystem == nil {
							t.Fatal("expected exact native permissions request")
						}
					} else if kind == "file-accept" {
						if request.Kind != FileApproval || request.File == nil {
							t.Fatal("expected file approval")
						}
					} else {
						if request.Kind != CommandApproval || request.Command == nil || request.Command.Command == nil || !strings.Contains(*request.Command.Command, "delidev-approval-fixture") || request.Command.Cwd == nil || *request.Command.Cwd != cfg.Process.Cwd {
							t.Fatal("native command approval changed exact requested command/cwd")
						}
					}
					arrival = event.Interaction.ID
					decision := ApprovalDecision{Kind: ApprovalAccept}
					if kind == "command-cancel" {
						decision.Kind = ApprovalCancel
					}
					var status InteractionStatus
					var err error
					if strings.HasPrefix(kind, "permissions-") {
						grant := PermissionGrant{Scope: PermissionTurn, Permissions: request.Permissions.Permissions}
						if kind == "permissions-session" {
							grant.Scope = PermissionSession
						}
						if kind == "permissions-empty" {
							grant.Permissions = PermissionProfile{}
						}
						if kind == "permissions-strict" {
							strict := true
							grant.StrictAutoReview = &strict
						}
						status, err = c.GrantPermissions(ctx, domain.NewID(), arrival, turn.TurnID, grant)
					} else {
						status, err = c.RespondApproval(ctx, domain.NewID(), arrival, turn.TurnID, decision)
					}
					if err != nil || status.Delivery != QuestionTransmitted || status.Accepted {
						t.Fatal("approval delivery failed or implied semantic acceptance", err)
					}
				}
				if event.Kind == InteractionClosedEvent {
					if arrival == "" || event.InteractionState.ID != arrival || event.InteractionState.Accepted || event.InteractionState.Delivery != QuestionTransmitted {
						t.Fatal("native approval resolution changed response ownership")
					}
					closed = true
				}
				if event.Kind == ApprovalAcceptedEvent {
					if !strings.HasPrefix(kind, "permissions-") || event.InteractionState == nil || event.InteractionState.ID != arrival || event.InteractionState.ApprovalEvidence != PermissionOutputEvidence || !event.InteractionState.Accepted || accepted {
						t.Fatal("foreign or duplicate native approval acceptance")
					}
					accepted = true
				}
				if event.Kind == ToolCompletedEvent {
					if kind == "command-cancel" && event.Tool.Status != ToolDeclined {
						t.Fatal("canceled native command was not declined")
					}
					toolCompleted = true
					if kind == "command-accept" || kind == "file-accept" {
						status, err := c.InspectInteraction(ctx, arrival)
						evidence := ApprovedCommandEvidence
						if kind == "file-accept" {
							evidence = ApprovedPatchEvidence
						}
						if err != nil || !status.Accepted || status.ApprovalEvidence != evidence {
							t.Fatal("single-use approval execution was not correlated", err)
						}
						accepted = true
					}
				}
				if event.Kind == TurnCompletedEvent {
					break
				}
			}
			status, err := c.InspectInteraction(ctx, arrival)
			if err != nil || !closed || (!toolCompleted && !strings.HasPrefix(kind, "permissions-")) || status.Accepted != expectAccepted || accepted != status.Accepted || c.execution.interactions.blocksInput() == accepted {
				t.Fatal("native approval lifecycle lost facts or inferred acceptance", err)
			}
			inspected, inspectErr := c.InspectInteractionResponse(ctx, status.ResponseID, arrival)
			if inspected != status || (inspectErr == nil) != expectAccepted {
				t.Fatal("native response inspection lost scope or invented acceptance", inspectErr)
			}
			if kind != "command-cancel" && (requests.Load() != 2 || !output.Load()) {
				t.Fatal("native tool output did not reach scripted provider")
			}
			if kind == "file-accept" {
				data, err := os.ReadFile(filepath.Join(cfg.Process.Cwd, "approval-fixture.txt"))
				if err != nil || string(data) != "delidev-approval-fixture\n" {
					t.Fatal("approved native patch did not write its private fixture", err)
				}
			}
			if err := c.Close(); err != nil {
				t.Fatal(err)
			}
			t.Logf("Codex %s %s: exact original request, one response, separate closure/tool completion; exact permission output or single-use execution proof; no remembered-policy inference or external account", SupportedVersion, kind)
		})
	}
}

func nativeApprovalProvider(t *testing.T, kind string) (*httptest.Server, *atomic.Int64, *atomic.Bool) {
	t.Helper()
	var requests atomic.Int64
	var output atomic.Bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := requests.Add(1)
		body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		var request struct {
			Model string
			Tools []struct{ Name, Type string }
			Input []struct {
				Type   string
				CallID string `json:"call_id"`
				Output json.RawMessage
			}
		}
		if r.Method != "POST" || r.URL.Path != "/responses" || err != nil || json.Unmarshal(body, &request) != nil || request.Model != "fixture-model" || count > 2 {
			t.Error("unexpected scripted approval request")
			http.Error(w, "invalid", 400)
			return
		}
		name := "exec_command"
		if strings.HasPrefix(kind, "permissions-") {
			name = "request_permissions"
		}
		if count == 1 {
			found := false
			for _, tool := range request.Tools {
				found = found || tool.Name == name
			}
			if !found {
				t.Errorf("native approval tool %s was not offered", name)
				http.Error(w, "unsupported", 400)
				return
			}
		} else {
			for _, item := range request.Input {
				if item.CallID == "call_approval_fixture" && (item.Type == "function_call_output" || item.Type == "custom_tool_call_output") {
					var text string
					if json.Unmarshal(item.Output, &text) == nil && text != "" && (kind != "command-accept" || strings.Contains(text, "delidev-approval-fixture")) {
						valid := true
						if strings.HasPrefix(kind, "permissions-") {
							var granted struct {
								Scope       PermissionGrantScope `json:"scope"`
								Strict      bool                 `json:"strict_auto_review"`
								Permissions struct {
									FileSystem struct {
										Write []string `json:"write"`
									} `json:"file_system"`
								} `json:"permissions"`
							}
							scope := PermissionTurn
							if kind == "permissions-session" {
								scope = PermissionSession
							}
							valid = json.Unmarshal([]byte(text), &granted) == nil && granted.Scope == scope && granted.Strict == (kind == "permissions-strict")
							if kind == "permissions-empty" {
								valid = valid && len(granted.Permissions.FileSystem.Write) == 0
							} else {
								valid = valid && len(granted.Permissions.FileSystem.Write) == 1 && filepath.IsAbs(granted.Permissions.FileSystem.Write[0])
							}
						}
						output.Store(valid)
					}
				}
			}
			if !output.Load() {
				t.Error("native tool output missing from scripted follow-up")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		send := func(event any) {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			w.(http.Flusher).Flush()
		}
		responseID := fmt.Sprintf("resp_approval_fixture_%d", count)
		send(map[string]any{"type": "response.created", "response": map[string]any{"id": responseID, "status": "in_progress"}})
		var item any
		if count == 1 {
			if kind == "file-accept" {
				args, _ := json.Marshal(map[string]any{"cmd": "apply_patch <<'PATCH'\n*** Begin Patch\n*** Add File: approval-fixture.txt\n+delidev-approval-fixture\n*** End Patch\nPATCH"})
				item = map[string]any{"type": "function_call", "id": "fc_approval_fixture", "call_id": "call_approval_fixture", "name": name, "arguments": string(args)}
			} else if strings.HasPrefix(kind, "permissions-") {
				args, _ := json.Marshal(map[string]any{"reason": "Private fixture workspace grant", "permissions": map[string]any{"file_system": map[string]any{"write": []string{"."}}}})
				item = map[string]any{"type": "function_call", "id": "fc_approval_fixture", "call_id": "call_approval_fixture", "name": name, "arguments": string(args)}
			} else {
				args, _ := json.Marshal(map[string]any{"cmd": "printf '%s' delidev-approval-fixture", "sandbox_permissions": "require_escalated", "justification": "Private local fixture approval", "prefix_rule": []string{"printf"}})
				item = map[string]any{"type": "function_call", "id": "fc_approval_fixture", "call_id": "call_approval_fixture", "name": name, "arguments": string(args)}
			}
		} else {
			item = map[string]any{"type": "message", "id": "msg_approval_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Native approval fixture complete."}}}
		}
		send(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		send(map[string]any{"type": "response.completed", "response": map[string]any{"id": responseID, "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}})
	}))
	t.Cleanup(provider.Close)
	return provider, &requests, &output
}
