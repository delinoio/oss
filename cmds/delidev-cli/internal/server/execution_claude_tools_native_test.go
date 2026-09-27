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
)

func TestManualNativeClaudePublishesOriginalReadTools(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, missing := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/missing-%t", mode, missing), func(t *testing.T) { nativePublishedClaudeRead(t, mode, missing) })
		}
	}
}

func nativePublishedClaudeRead(t *testing.T, mode domain.SessionMode, missing bool) {
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
	path := filepath.Join(workdir, "original.txt")
	if !missing {
		if err := os.WriteFile(path, []byte("Original private Read evidence.\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	originalInput, _ := json.Marshal(map[string]any{"file_path": path})
	var calls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("X-Api-Key") != "" {
			t.Error("native tool request escaped registered authority")
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if err != nil {
			t.Error(err)
			return
		}
		n := calls.Add(1)
		if n > 2 {
			t.Error("tool publication repeated inference")
			w.WriteHeader(400)
			return
		}
		if n == 2 {
			var request struct {
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if json.Unmarshal(raw, &request) != nil {
				t.Error("invalid native request")
				return
			}
			matched := 0
			for _, m := range request.Messages {
				if m.Role != "user" {
					continue
				}
				var blocks []struct {
					Type    string          `json:"type"`
					Tool    string          `json:"tool_use_id"`
					Error   *bool           `json:"is_error"`
					Content json.RawMessage `json:"content"`
				}
				if json.Unmarshal(m.Content, &blocks) != nil {
					continue
				}
				for _, b := range blocks {
					if b.Type != "tool_result" {
						continue
					}
					if b.Tool != "toolu_original_read" || (b.Error != nil && *b.Error) != missing || !missing && !strings.Contains(string(b.Content), "Original private Read evidence.") {
						t.Error("original tool result changed before provider continuation")
						w.WriteHeader(400)
						return
					}
					matched++
				}
			}
			if matched != 1 {
				t.Error("native request lost original result")
				w.WriteHeader(400)
				return
			}
		}
		kind, reason := "text", "end_turn"
		block := map[string]any{"type": kind, "text": ""}
		delta := map[string]any{"type": "text_delta", "text": "Original tool outcome observed."}
		if n == 1 {
			kind, reason = "tool_use", "tool_use"
			block = map[string]any{"type": kind, "id": "toolu_original_read", "name": "Read", "input": map[string]any{}}
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(originalInput)}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_tool_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": block},
			{"type": "content_block_delta", "index": 0, "delta": delta},
			{"type": "content_block_stop", "index": 0},
			{"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}},
			{"type": "message_stop"},
		} {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
		}
	}))
	defer upstream.Close()
	f := newProfileAuthorityFixture(t, upstream.URL, domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false)
	f.registerGrant(t)
	pc := publicationWorkerConfig(t, publicationFixtureFromAuthority(t, f))
	pc.Root = filepath.Join(root, "worker")
	// Drop the nested proposal or independent result acknowledgment. Both must
	// preserve one native call and exactly the same durable server request.
	loss := 11
	if missing {
		loss = 15
	}
	publication := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(pc.Root, "jobs", string(f.job), "publication.json"), dropAt: loss}
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
	replay := func(err error) {
		t.Helper()
		if err == nil {
			return
		}
		if display == nil || len(publication.calls) != loss {
			t.Fatal("unexpected original publication failure", err, len(publication.calls))
		}
		if err := display.ReplayPending(ctx); err != nil {
			t.Fatal(err)
		}
	}
	accepted, results := 0, 0
	for {
		o, err := s.Next(ctx)
		if err != nil {
			t.Fatal("native Read observation", err)
		}
		switch o.Kind {
		case claude.SessionInitialized:
			if err := binding.BindSession(ctx, o, applied); err != nil {
				t.Fatal(err)
			}
		case claude.InputAccepted:
			accepted++
			if err := binding.AcceptInput(ctx, o); err != nil {
				t.Fatal(err)
			}
			display, err = worker.OpenClaudeContentPublisher(binding)
			if err != nil {
				t.Fatal(err)
			}
			replay(display.PublishInput(ctx))
		case claude.ContentObserved:
			if display == nil {
				t.Fatal("content preceded accepted input")
			}
			handled, err := display.PublishObservation(ctx, o)
			if !handled {
				t.Fatal("tool content unhandled")
			}
			replay(err)
			_, err = display.PublishUsageObservation(ctx, o)
			replay(err)
		case claude.InputFinished:
			if o.InputID != f.input.InputID || o.Result == nil || !o.Result.Successful() {
				t.Fatal("tool error was confused with original root outcome")
			}
			results++
			_, err = display.PublishUsageObservation(ctx, o)
			replay(err)
		case claude.InteractionObserved, claude.TaskObserved:
			t.Fatal("Read fixture introduced unhandled interaction/child")
		}
		if o.Kind == claude.RunStateObserved && o.Run.State == claude.RunIdle {
			break
		}
	}
	if accepted != 1 || results != 1 || calls.Load() != 2 {
		t.Fatal("original input, tool or inference repeated")
	}
	closed, err := s.CloseForContinuation(ctx)
	if missing {
		// Error-result publication is supported, but this private checkpoint
		// profile does not yet prove restoration of failed native Read history.
		// Preserve that boundary rather than promoting display evidence.
		problem, ok := err.(*domain.Error)
		if !ok || problem.Code != domain.Unsupported || closed != nil {
			t.Fatal("failed Read unexpectedly granted continuation", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		if err != nil {
			t.Fatal("native tool history did not close", err)
		}
		if _, _, err := closed.RetainCheckpoint(ctx); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 4 {
		t.Fatal("original user/provider/tool message count", len(rows), err)
	}
	tools := 0
	for _, r := range rows {
		v, err := store.Decode[domain.ExecutionMessage](r)
		if err != nil || v.State != domain.MessageComplete {
			t.Fatal("original message did not close", err)
		}
		if v.Role != domain.ToolMessage {
			continue
		}
		tools++
		c := v.ClaudeTool
		if c == nil || c.Reference.ID != r.ID || c.Reference.NativeID != "toolu_original_read" || v.NativeParentID != "msg_tool_1" || c.Proposal == nil || c.Proposal.Proposed != string(originalInput) || c.Proposal.Applied != string(originalInput) || c.Result == nil || (c.Result.Error != nil && *c.Result.Error) != missing || !missing && (c.Result.Text == nil || !strings.Contains(*c.Result.Text, "Original private Read evidence.")) {
			t.Fatal("original tool bytes/ownership/result lost")
		}
	}
	if tools != 1 {
		t.Fatal("original tool duplicated")
	}
	r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](r)
	if err != nil || session.Execution.Outcome != domain.ExecutionRunning || session.Execution.CleanupVerified || uint64(len(publication.calls)) != session.Execution.LastSequence+1 || publication.calls[loss-1] != publication.calls[loss] {
		t.Fatal("tool receipt changed or inferred terminal authority", err)
	}
}
