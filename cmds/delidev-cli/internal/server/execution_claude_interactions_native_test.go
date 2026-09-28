package server

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
)

func TestManualNativeClaudePublishesOriginalCallbacks(t *testing.T) {
	for _, test := range []struct {
		name string
		mode domain.SessionMode
		tool string
	}{{"permission", domain.ExecuteMode, "Bash"}, {"question-execute", domain.ExecuteMode, "AskUserQuestion"}, {"question-plan", domain.PlanMode, "AskUserQuestion"}} {
		t.Run(test.name, func(t *testing.T) { nativeClaudeCallback(t, test.mode, test.tool) })
	}
}
func nativeClaudeCallback(t *testing.T, mode domain.SessionMode, name string) {
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
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if calls.Add(1) != 1 || r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("X-Api-Key") != "" {
			t.Error("callback fixture escaped original registered inference")
			w.WriteHeader(400)
			return
		}
		_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, 8<<20))
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_callback_original", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "tool_use", "id": "toolu_callback_original", "name": name, "input": map[string]any{}}},
			{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "input_json_delta", "partial_json": string(input)}},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
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
	cfg := claude.APIStreamConfig{Process: process.Config{Directory: filepath.Join(root, "processes"), OwnerID: f.job, Executable: binary, Cwd: runtimeRoot, Env: env, Logger: f.service.logger}, Version: claude.SupportedVersion, Home: filepath.Join(runtimeRoot, "claude"), Workspace: workdir, SessionID: f.input.SessionID, Model: f.input.Configuration.NativeModel, Permission: claude.NativePermission(permission), API: claude.APIConfig{ServerOrigin: f.http.URL, Token: f.token}}
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
	canceled := false
	for !canceled {
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
			if _, err := display.PublishObservation(ctx, o); err != nil {
				t.Fatal("native callback tool publication", err)
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
				_, err = s.Interrupt(ctx, domain.NewID(), func(_ context.Context, claim claude.InterruptClaim) error {
					raw, err := json.Marshal(claim)
					if err != nil {
						return err
					}
					return security.WriteAtomic(filepath.Join(root, "original-interrupt.json"), raw)
				})
				if err != nil {
					t.Fatal("original callback Stop", err)
				}
			} else if o.Interaction.Kind == claude.InteractionCanceled {
				handled, err := display.PublishInteractionObservation(ctx, o)
				if !handled || err != nil {
					t.Fatal("original callback cancellation", err)
				}
				canceled = true
			} else {
				t.Fatal("fixture fabricated an approval response")
			}
		case claude.InputFinished:
			t.Fatal("callback completed before original cancellation")
		}
	}
	if original == nil || calls.Load() != 1 {
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
	if err != nil || value.Claude == nil || value.Claude.ArrivalID != original.ArrivalID || value.NativeRequestID.Text != original.RequestID || value.Claude.InputJSON != string(original.Input) || value.Claude.Tool.Name != name || value.Closure != domain.InteractionNativeClosed || value.ClaudeCancellation == nil || value.ClaudeCancellation.ArrivalID != original.ArrivalID || value.Response != nil || value.ApprovalResponse != nil {
		t.Fatal("original callback or independent cancellation changed", err)
	}
	if _, err := os.Stat(filepath.Join(workdir, "callback-marker.txt")); !os.IsNotExist(err) {
		t.Fatal("observation executed the unapproved native command")
	}
}
