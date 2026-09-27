package server

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
	"regexp"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestManualNativeClaudeOriginalReplyDelivery(t *testing.T) {
	for _, test := range []struct {
		name string
		mode domain.SessionMode
		tool string
		deny bool
	}{
		{"allow-tool", domain.ExecuteMode, "Bash", false}, {"deny-tool", domain.ExecuteMode, "Bash", true},
		{"answer-execute", domain.ExecuteMode, "AskUserQuestion", false}, {"answer-plan", domain.PlanMode, "AskUserQuestion", false},
		{"deny-question", domain.ExecuteMode, "AskUserQuestion", true},
		{"allow-plan-approval", domain.PlanMode, "ExitPlanMode", false}, {"deny-plan-approval", domain.PlanMode, "ExitPlanMode", true},
	} {
		t.Run(test.name, func(t *testing.T) { nativeClaudeReply(t, test.mode, test.tool, test.deny) })
	}
}
func nativeClaudeReply(t *testing.T, mode domain.SessionMode, name string, deny bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary and private scripted provider required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtimeRoot, workdir := filepath.Join(root, "runtime"), filepath.Join(root, "workspace")
	env, err := harness.PrivateRuntimeEnvironment(runtimeRoot)
	if err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(workdir); err != nil {
		t.Fatal(err)
	}
	params := map[string]any{"command": "printf original > callback-marker.txt"}
	if name == "AskUserQuestion" {
		params = map[string]any{"questions": []any{map[string]any{"question": "Which original option?", "header": "Choice", "multiSelect": false, "options": []any{map[string]any{"label": "One", "description": "First original option"}, map[string]any{"label": "Two", "description": "Second original option"}}}}}
	}
	input, _ := json.Marshal(params)
	const plan = "# Original plan\nPreserve the exact original callback and verify its result.\n"
	var planPath atomic.Value
	planPath.Store("")
	expectedRequests := int32(2)
	if name == "ExitPlanMode" {
		expectedRequests = 3
	}
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		n := calls.Add(1)
		if n > expectedRequests || r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("X-Api-Key") != "" {
			t.Error("callback fixture escaped original registered inference")
			w.WriteHeader(400)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if n == 2 && name == "AskUserQuestion" && !deny && !strings.Contains(string(raw), "Two") {
			t.Error("native continuation lost the original answer")
		}
		block := map[string]any{"type": "tool_use", "id": "toolu_callback_original", "name": name, "input": map[string]any{}}
		delta := map[string]any{"type": "input_json_delta", "partial_json": string(input)}
		reason := "tool_use"
		if n == expectedRequests {
			block = map[string]any{"type": "text", "text": ""}
			delta = map[string]any{"type": "text_delta", "text": "Original callback completed."}
			reason = "end_turn"
		}
		if name == "ExitPlanMode" && n < expectedRequests {
			if n == 1 {
				var body struct {
					Messages []struct {
						Content json.RawMessage `json:"content"`
					} `json:"messages"`
				}
				if json.Unmarshal(raw, &body) != nil {
					t.Error("invalid Plan provider request")
					w.WriteHeader(400)
					return
				}
				var text strings.Builder
				for _, m := range body.Messages {
					var plain string
					if json.Unmarshal(m.Content, &plain) == nil {
						text.WriteString(plain)
						continue
					}
					var blocks []struct {
						Text string `json:"text"`
					}
					if json.Unmarshal(m.Content, &blocks) == nil {
						for _, b := range blocks {
							text.WriteString(b.Text)
						}
					}
				}
				matches := regexp.MustCompile(`You should create your plan at (.+?) using the Write tool\.`).FindAllStringSubmatch(text.String(), -1)
				if len(matches) != 1 || filepath.Dir(matches[0][1]) != filepath.Join(runtimeRoot, "claude", "plans") || filepath.Ext(matches[0][1]) != ".md" {
					t.Error("native Plan context lost its private artifact")
					w.WriteHeader(400)
					return
				}
				planPath.Store(matches[0][1])
				block = map[string]any{"type": "tool_use", "id": "toolu_plan_write", "name": "Write", "input": map[string]any{}}
				written, _ := json.Marshal(map[string]any{"file_path": matches[0][1], "content": plan})
				delta = map[string]any{"type": "input_json_delta", "partial_json": string(written)}
			} else {
				bytes, err := os.ReadFile(planPath.Load().(string))
				if err != nil || string(bytes) != plan {
					t.Error("original native plan artifact changed")
					w.WriteHeader(400)
					return
				}
				delta = map[string]any{"type": "input_json_delta", "partial_json": "{}"}
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_callback_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": block},
			{"type": "content_block_delta", "index": 0, "delta": delta},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
			{"type": "message_stop"},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	}))
	defer upstream.Close()
	f := newProfileAuthorityFixture(t, upstream.URL, domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false)
	f.registerGrant(t)
	pc := publicationWorkerConfig(t, publicationFixtureFromAuthority(t, f))
	pc.Root = filepath.Join(root, "worker")
	publication := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(pc.Root, "jobs", string(f.job), "publication.json"), dropAt: 999}
	pc.Client = publication
	p, err := worker.OpenExecutionPublisher(pc)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	binding, err := worker.OpenClaudeBindingPublisher(p)
	if err != nil {
		t.Fatal(err)
	}
	defer binding.Close()
	permission, err := f.input.Configuration.ClaudeAPIInputPermission(mode)
	if err != nil {
		t.Fatal(err)
	}
	cfg := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: slog.New(slog.NewTextHandler(os.Stderr, nil))}, Version: claude.SupportedVersion, Home: filepath.Join(runtimeRoot, "claude"), Workspace: workdir, SessionID: f.input.SessionID, Model: f.input.Configuration.NativeModel, Permission: claude.NativePermission(permission), API: claude.APIConfig{ServerOrigin: f.http.URL, Token: f.token}}
	s, err := claude.OpenAPISession(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if err := process.ReconcileOwner(cfg.Process.Directory, f.job); err != nil {
			t.Error(err)
		}
	}()
	if err := binding.ClaimInput(ctx, f.input.TurnRequestID, f.input.InputID, f.input.Input.Prompt); err != nil {
		t.Fatal(err)
	}
	applied, err := s.SendInput(ctx, f.input.InputID, f.input.Input.Prompt, claude.ContinueSuccessfulRun)
	if err != nil {
		t.Fatal(err)
	}
	var display *worker.ClaudeContentPublisher
	var original *claude.NativeInteraction
	lost := 0
	finished, echoed := false, false
	for !finished {
		o, err := s.Next(ctx)
		if err != nil {
			t.Fatal("native callback lifecycle", err)
		}
		switch o.Kind {
		case claude.SessionInitialized:
			if err := binding.BindSession(ctx, o, applied); err != nil {
				t.Fatal(err)
			}
		case claude.InputAccepted:
			if err := binding.AcceptInput(ctx, o); err != nil {
				t.Fatal(err)
			}
			display, err = worker.OpenClaudeContentPublisher(binding)
			if err != nil {
				t.Fatal(err)
			}
			if err := display.PublishInput(ctx); err != nil {
				t.Fatal(err)
			}
		case claude.ContentObserved:
			if display == nil {
				t.Fatal("content preceded acceptance")
			}
			settles := original != nil && len(o.Content) == 1 && o.Content[0].Kind == claude.ToolResultObserved && o.Content[0].ToolResult.ID == original.ToolID
			if settles {
				publication.dropAt = len(publication.calls) + 2
			}
			_, publishErr := display.PublishObservation(ctx, o)
			if settles {
				lost = publication.dropAt
				if publishErr == nil || len(publication.calls) != lost {
					t.Fatal("settlement acknowledgment not lost", publishErr)
				}
				if err := display.ReplayPending(ctx); err != nil {
					t.Fatal("settlement receipt replay", err)
				}
				if publication.calls[lost-1] != publication.calls[lost] {
					t.Fatal("settlement receipt changed")
				}
			} else if publishErr != nil {
				t.Fatal("native callback tool publication", publishErr)
			}
		case claude.InteractionObserved:
			if display == nil {
				t.Fatal("callback preceded original acceptance")
			}
			if o.Interaction.Kind == claude.InteractionRequested {
				if original != nil {
					t.Fatal("original callback repeated")
				}
				original = o.Interaction.Request
				if name == "ExitPlanMode" && (original.Kind != claude.PlanApproval || original.Plan == nil || *original.Plan != plan || original.PlanPath == nil || *original.PlanPath != planPath.Load().(string)) {
					t.Fatal("native Plan callback lost original artifact")
				}
				lost = len(publication.calls) + 1
				publication.dropAt = lost
				handled, err := display.PublishInteractionObservation(ctx, o)
				if !handled || err == nil || len(publication.calls) != lost {
					t.Fatal("callback acknowledgment was not lost exactly", err)
				}
				if err := display.ReplayPending(ctx); err != nil {
					t.Fatal(err)
				}
				if publication.calls[lost-1] != publication.calls[lost] {
					t.Fatal("callback receipt changed on replay")
				}
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
				if err != nil || len(rows) != 1 {
					t.Fatal("original request not published", err)
				}
				pf := &publicationFixture{authorityFixture: f}
				responseID := domain.NewID()
				reply := &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}
				if name == "AskUserQuestion" {
					reply.Answers = map[string]string{"Which original option?": "Two"}
				}
				if deny {
					message := "Original request denied"
					reply = &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyDeny, Message: &message}
				}
				var deliver func() error
				if name == "AskUserQuestion" {
					if _, err := acceptFixtureResponse(pf, responseID, rows[0].ID, rows[0].Revision, domain.QuestionResponseInput{Claude: reply}); err != nil {
						t.Fatal(err)
					}
					control := &pb.QuestionResponseControl{JobId: string(f.job), InteractionId: string(rows[0].ID), ResponseId: string(responseID), Revision: rows[0].Revision + 1}
					deliver = func() error { return display.DeliverQuestionResponse(ctx, ctx, control, s) }
				} else {
					if _, err := acceptFixtureApproval(pf, responseID, rows[0].ID, rows[0].Revision, domain.ApprovalResponseInput{Claude: reply}); err != nil {
						t.Fatal(err)
					}
					control := &pb.ApprovalResponseControl{JobId: string(f.job), InteractionId: string(rows[0].ID), ResponseId: string(responseID), Revision: rows[0].Revision + 1}
					deliver = func() error { return display.DeliverApprovalResponse(ctx, ctx, control, s) }
				}
				lost = len(publication.calls) + 1
				publication.dropAt = lost
				if err := deliver(); err == nil || len(publication.calls) != lost {
					t.Fatal("delivery acknowledgment not lost", err)
				}
				if err := display.ReplayPending(ctx); err != nil {
					t.Fatal("delivery receipt replay", err)
				}
				if publication.calls[lost-1] != publication.calls[lost] {
					t.Fatal("delivery receipt changed")
				}
				// The identical control cannot call the once-only native reply again.
				if err := deliver(); err != nil {
					t.Fatal("duplicate control attempted another response", err)
				}

			} else if o.Interaction.Kind == claude.InteractionReplyEchoed {
				lost = len(publication.calls) + 1
				publication.dropAt = lost
				if handled, err := display.PublishInteractionObservation(ctx, o); !handled || err == nil || len(publication.calls) != lost {
					t.Fatal("echo acknowledgment not lost", err)
				}
				if err := display.ReplayPending(ctx); err != nil {
					t.Fatal("echo receipt replay", err)
				}
				if publication.calls[lost-1] != publication.calls[lost] {
					t.Fatal("echo receipt changed")
				}
				echoed = true
			} else {
				t.Fatal("native reply unexpectedly canceled")
			}
		case claude.InputFinished:
			if !echoed {
				t.Fatal("root result preceded original reply echo")
			}
			if handled, err := display.PublishUsageObservation(ctx, o); !handled || err != nil {
				t.Fatal("settled callbacks blocked original result usage", err)
			}
			finished = true
		}
	}
	if original == nil || calls.Load() != expectedRequests {
		t.Fatal("original callback missing or inference repeated")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("original callback record count", len(rows), err)
	}
	value, err := store.Decode[domain.ExecutionInteraction](rows[0])
	if err != nil || value.Claude == nil || value.Claude.ArrivalID != original.ArrivalID || value.NativeRequestID.Text != original.RequestID || value.Claude.InputJSON != string(original.Input) || value.Claude.Tool.Name != name || value.Closure != domain.InteractionNativeClosed || value.ClaudeCancellation != nil || value.ClaudeSettlement == nil {
		t.Fatal("original request changed", err)
	}
	if name == "AskUserQuestion" {
		if value.Response == nil || value.ApprovalResponse != nil || value.Response.State != domain.QuestionResponseAccepted || value.Response.Acceptance == nil || value.Response.ClaudeEcho == nil {
			t.Fatal("question result did not settle original callback")
		}
	} else if value.ApprovalResponse == nil || value.Response != nil || value.ApprovalResponse.State != domain.ApprovalResponseAccepted || value.ApprovalResponse.Acceptance == nil || value.ApprovalResponse.ClaudeEcho == nil {
		t.Fatal("approval result did not settle original callback")
	}
	marker, err := os.ReadFile(filepath.Join(workdir, "callback-marker.txt"))
	if name == "Bash" && !deny {
		if err != nil || string(marker) != "original" {
			t.Fatal("original allowed command did not run", err)
		}
	} else if !os.IsNotExist(err) {
		t.Fatal("denied or unrelated command executed")
	}
}
