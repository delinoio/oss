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

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/claude"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// Readiness and immutable assignments are fixture state. Registration, the
// protected-key lookup, revocable relay and native process are real. The local
// claim is a test coordinator, not evidence of public Worker publication.
func TestManualNativeClaudeUsesRegisteredServerRelay(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeRegisteredClaude(t, mode, false, 0, false, false) })
	}
}

func TestManualNativeClaudeRegisteredRelayRevocation(t *testing.T) {
	nativeRegisteredClaude(t, domain.ExecuteMode, true, 0, false, false)
}

func TestManualNativeClaudePublishesOriginalBindings(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, lost := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s/lost-%d", mode, lost), func(t *testing.T) { nativeRegisteredClaude(t, mode, false, lost, false, false) })
		}
	}
}

func TestManualNativeClaudePublishesOrderedContent(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, lost := range []int{3, 7} {
			t.Run(fmt.Sprintf("%s/lost-%d", mode, lost), func(t *testing.T) { nativeRegisteredClaude(t, mode, false, lost, true, false) })
		}
	}
}

func TestManualNativeClaudePublishesUsage(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, lost := range []int{6, 29} {
			t.Run(fmt.Sprintf("%s/lost-%d", mode, lost), func(t *testing.T) { nativeRegisteredClaude(t, mode, false, lost, true, true) })
		}
	}
}

func nativeRegisteredClaude(t *testing.T, mode domain.SessionMode, revoke bool, publicationLoss int, content, usage bool) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native binary and private scripted provider required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var calls atomic.Int32
	started, ended := make(chan struct{}), make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if calls.Add(1) != 1 || r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" || r.Header.Get("X-Api-Key") != "" || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("native Claude request escaped its exact registered authority")
			http.Error(w, "invalid fixture request", http.StatusBadRequest)
			return
		}
		var request struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if json.NewDecoder(io.LimitReader(r.Body, 8<<20)).Decode(&request) != nil || request.Model != "fixture-model" || !request.Stream || len(request.Messages) != 2 || request.Messages[0].Role != "user" || request.Messages[1].Role != "system" {
			t.Error("native Claude model or original input changed")
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The pinned full-mode CLI appends its native tool/context system
		// message. It is not a second product input or original-input proof.
		var nativeSystem []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(request.Messages[1].Content, &nativeSystem) != nil || len(nativeSystem) == 0 {
			t.Error("native system context has no structured text")
		}
		for _, block := range nativeSystem {
			if block.Type != "text" || block.Text == "" {
				t.Error("unexpected native system context block")
			}
		}
		promptCount := 0
		var text string
		var blocks []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		}
		if json.Unmarshal(request.Messages[0].Content, &text) == nil {
			if text == "Fixture prompt" {
				promptCount++
			}
		} else if json.Unmarshal(request.Messages[0].Content, &blocks) != nil {
			t.Error("unexpected native input content representation")
		}
		for _, block := range blocks {
			if block.Type == "text" && block.Text == "Fixture prompt" {
				promptCount++
			} else if block.Type != "text" || !strings.HasPrefix(block.Text, "<system-reminder>\n") || !strings.HasSuffix(block.Text, "</system-reminder>\n\n") {
				t.Error("unexpected original Claude input context")
			}
		}
		if promptCount != 1 {
			t.Error("native Claude original prompt missing or duplicated")
		}
		close(started)
		defer close(ended)
		if revoke {
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		events := []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": "msg_registered_claude", "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 3, "output_tokens": 0}}},
		}
		index := 0
		if content {
			events = append(events,
				map[string]any{"type": "content_block_start", "index": 0, "content_block": map[string]any{"type": "thinking", "thinking": "", "signature": ""}},
				map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "thinking_delta", "thinking": "Original native reasoning."}},
				map[string]any{"type": "content_block_delta", "index": 0, "delta": map[string]any{"type": "signature_delta", "signature": "private-fixture-signature"}},
				map[string]any{"type": "content_block_stop", "index": 0},
				map[string]any{"type": "content_block_start", "index": 1, "content_block": map[string]any{"type": "redacted_thinking", "data": "private-fixture-redaction"}},
				map[string]any{"type": "content_block_stop", "index": 1},
				map[string]any{"type": "content_block_start", "index": 2, "content_block": map[string]any{"type": "text", "text": ""}},
				map[string]any{"type": "content_block_delta", "index": 2, "delta": map[string]any{"type": "text_delta", "text": "First native block."}},
				map[string]any{"type": "content_block_stop", "index": 2},
			)
			index = 3
		}
		events = append(events,
			map[string]any{"type": "content_block_start", "index": index, "content_block": map[string]any{"type": "text", "text": ""}},
			map[string]any{"type": "content_block_delta", "index": index, "delta": map[string]any{"type": "text_delta", "text": "Registered Claude result."}},
			map[string]any{"type": "content_block_stop", "index": index},
			map[string]any{"type": "message_delta", "delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 4}},
			map[string]any{"type": "message_stop"},
		)
		for _, event := range events {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], raw)
		}
	}))
	defer upstream.Close()
	f := newProfileAuthorityFixture(t, upstream.URL, domain.ClaudeCode, domain.AnthropicMessages, func(input *domain.ExecutionJobInput) { input.Input.Mode = mode }, false)
	f.registerGrant(t)
	var bindings *worker.ClaudeBindingPublisher
	var publication *losePublicationAck
	if publicationLoss != 0 {
		pf := publicationFixtureFromAuthority(t, f)
		pc := publicationWorkerConfig(t, pf)
		privateParent, err := filepath.EvalSymlinks(t.TempDir())
		if err != nil {
			t.Fatal(err)
		}
		pc.Root = filepath.Join(privateParent, "worker")
		publication = &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(pc.Root, "jobs", string(f.job), "publication.json"), dropAt: publicationLoss}
		pc.Client = publication
		publisher, err := worker.OpenExecutionPublisher(pc)
		if err != nil {
			t.Fatal(err)
		}
		defer publisher.Close()
		bindings, err = worker.OpenClaudeBindingPublisher(publisher)
		if err != nil {
			t.Fatal(err)
		}
		defer bindings.Close()
	}
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
	claim, _ := json.Marshal(struct{ Job, Session, Input, Request domain.ID }{f.job, f.input.SessionID, f.input.InputID, f.input.TurnRequestID})
	if err := security.WriteAtomic(filepath.Join(root, "claimed-input.json"), claim); err != nil {
		t.Fatal(err)
	}
	if bindings != nil {
		if err := bindings.ClaimInput(ctx, f.input.TurnRequestID, f.input.InputID, f.input.Input.Prompt); err != nil {
			t.Fatal(err)
		}
	}
	applied, err := s.SendInput(ctx, f.input.InputID, f.input.Input.Prompt, claude.ContinueSuccessfulRun)
	if err != nil || applied.Model != cfg.Model || applied.Effort == nil {
		t.Fatal("original native settings/input were not verified", err)
	}
	if revoke {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("registered native request did not start")
		}
		client := delidevv1connect.NewAccountServiceClient(http.DefaultClient, f.http.URL)
		disconnected, err := client.DisconnectAccount(ctx, ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}))
		if err != nil || len(disconnected.Msg.CleanupProblemJson) != 0 {
			t.Fatal("account disconnection failed to join original relay", err)
		}
		select {
		case <-ended:
		case <-ctx.Done():
			t.Fatal("revoked native upstream request remained active")
		}
		if _, _, retained := f.service.accountSecrets.(*accountTestSecrets).counts(); retained != 0 {
			t.Fatal("disconnected account retained the protected fixture key")
		}
		request := connect.NewRequest(f.register)
		request.Header().Set("Authorization", "Bearer "+f.workerToken)
		if _, err := f.client.RegisterExecution(ctx, request); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("original receipt restored revoked native authority")
		}
		response := f.requestPath(t, f.token, "/messages?beta=true", `{"model":"fixture-model"}`)
		response.Body.Close()
		if response.StatusCode != http.StatusForbidden {
			t.Fatal("revoked native execution token remained usable")
		}
		// Revocation proves relay containment only. Do not infer a native
		// result or send an unclaimed interrupt; join the original process.
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	} else {
		accepted, completed, texts, estimates := 0, 0, 0, 0
		var display *worker.ClaudeContentPublisher
		for {
			o, err := s.Next(ctx)
			if err != nil {
				t.Fatal("next original native observation", accepted, completed, texts, err)
			}
			if bindings != nil && (o.Kind == claude.SessionInitialized || o.Kind == claude.InputAccepted) {
				var err error
				if o.Kind == claude.SessionInitialized {
					err = bindings.BindSession(ctx, o, applied)
				} else {
					err = bindings.AcceptInput(ctx, o)
				}
				if err != nil {
					if len(publication.calls) != publicationLoss {
						t.Fatal("unexpected original publication failure", err)
					}
					if err := bindings.ReplayPending(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			if content && o.Kind == claude.InputAccepted {
				display, err = worker.OpenClaudeContentPublisher(bindings)
				if err != nil {
					t.Fatal(err)
				}
				if err := display.PublishInput(ctx); err != nil {
					if len(publication.calls) != publicationLoss {
						t.Fatal("unexpected original input publication failure", err)
					}
					if err := display.ReplayPending(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			if content && o.Kind == claude.ContentObserved {
				if display == nil {
					t.Fatal("content preceded original input acceptance")
				}
				handled, err := display.PublishObservation(ctx, o)
				if !handled {
					t.Fatal("original content was not consumed")
				}
				if err != nil {
					if len(publication.calls) != publicationLoss {
						t.Fatal("unexpected native content publication failure", o.Kind, err)
					}
					if err := display.ReplayPending(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			if usage && (o.Kind == claude.ContentObserved || o.Kind == claude.InputFinished) {
				_, err := display.PublishUsageObservation(ctx, o)
				if err != nil {
					if len(publication.calls) != publicationLoss {
						t.Fatal("unexpected native usage publication failure", err)
					}
					if err := display.ReplayPending(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}

			if o.Kind == claude.InputAccepted {
				if o.InputID != f.input.InputID || o.SessionID != f.input.SessionID || o.TurnID == "" {
					t.Fatal("native acceptance lost original ownership")
				}
				accepted++
			}
			for _, c := range o.Content {
				if c.Kind == claude.ContentCompleted && c.Block != nil && c.Block.Kind == claude.TextBlock {
					expected := "Registered Claude result."
					if content && texts == 0 {
						expected = "First native block."
					}
					if c.Block.Text == nil || *c.Block.Text != expected || c.MessageID != "msg_registered_claude" {
						t.Fatal("registered native output changed")
					}
					texts++
				}
			}
			if o.Progress != nil && o.Progress.Kind == claude.ThinkingTokensEstimated {
				if o.Progress.Thinking == nil || o.Progress.Thinking.Tokens == 0 || o.Progress.Thinking.Delta == 0 {
					t.Fatal("native thinking estimate lost its separate counters")
				}
				estimates++
			}
			if o.Kind == claude.InputFinished {
				if o.InputID != f.input.InputID || o.Result == nil || !o.Result.Successful() {
					t.Fatal("original native result was not successful")
				}
				completed++
			}
			if o.Kind == claude.RunStateObserved && o.Run.State == claude.RunIdle {
				break
			}
		}
		expectedTexts := 1
		if content {
			expectedTexts = 2
		}
		if accepted != 1 || completed != 1 || texts != expectedTexts || (content && estimates == 0) {
			t.Fatal("registered Claude run lost or repeated original facts", accepted, completed, texts)
		}
		closed, err := s.CloseForContinuation(ctx)
		if err != nil {
			t.Fatal("registered native run did not preserve closed history", err)
		}
		if _, _, err := closed.RetainCheckpoint(ctx); err != nil {
			t.Fatal("registered native history did not retain original evidence", err)
		}
	}
	if bindings != nil {
		r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.Decode[domain.Session](r)
		expectedSequence := uint64(2)
		if content {
			expectedSequence = 22
		}
		if usage {
			expectedSequence = 29
		}
		if err != nil || session.Execution == nil || session.Execution.LastSequence != expectedSequence || session.Execution.NativeThreadID != string(f.input.SessionID) || session.Execution.NativeTurnID == "" || session.PendingInputs != 0 || session.PendingInputBytes != 0 || session.Execution.CleanupVerified || session.Execution.Outcome != domain.ExecutionRunning || uint64(len(publication.calls)) != expectedSequence+1 || publication.calls[publicationLoss-1] != publication.calls[publicationLoss] {
			t.Fatal("original bindings lost ownership/accounting or fabricated terminal evidence", err)
		}
	}
	if content {
		records, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
		if err != nil || len(records) != 2 {
			t.Fatal("native transcript lost original input/provider messages", err, len(records))
		}
		users, assistants := 0, 0
		for _, record := range records {
			message, err := store.Decode[domain.ExecutionMessage](record)
			if err != nil {
				t.Fatal(err)
			}
			if message.State != domain.MessageComplete {
				t.Fatal("original native message did not close")
			}
			if message.Role == domain.UserMessage {
				users++
				if message.Text != "Fixture prompt" || message.NativeID != string(f.input.InputID) || message.Claude != nil {
					t.Fatal("original user replay changed")
				}
				continue
			}
			assistants++
			if message.NativeID != "msg_registered_claude" || message.Text != "" || message.Claude == nil || len(message.Claude.Blocks) != 4 || message.Claude.StopReason == nil || *message.Claude.StopReason != "end_turn" || message.Claude.StopSequence != nil {
				t.Fatal("native provider message identity or stop metadata changed")
			}
			kinds := []domain.ClaudeTextKind{domain.ClaudeThinking, domain.ClaudeRedactedThinking, domain.ClaudeText, domain.ClaudeText}
			texts := []string{"Original native reasoning.", "", "First native block.", "Registered Claude result."}
			for i, block := range message.Claude.Blocks {
				if block.Index != uint32(i) || block.State != domain.ClaudeBlockStopped || block.Block.Kind != kinds[i] || block.Block.Text != texts[i] {
					t.Fatal("native block order or bytes changed", i)
				}
			}
			raw, _ := json.Marshal(message)
			if strings.Contains(string(raw), "private-fixture") {
				t.Fatal("opaque native data entered public transcript")
			}
		}
		if users != 1 || assistants != 1 {
			t.Fatal("native transcript roles were duplicated")
		}
	}
	if usage {
		records, err := f.service.Store.List(ctx, store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 20})
		if err != nil || len(records) != 7 {
			t.Fatal("native usage source count changed", len(records), err)
		}
		sources := map[domain.ClaudeUsageSource]int{}
		for _, r := range records {
			u, err := store.Decode[domain.ClaudeUsageRecord](r)
			if err != nil || u.Usage.Validate() != nil || u.AccountID != f.input.AccountID || u.ConnectionID != f.input.ConnectionID || u.ExecutionID != f.input.ExecutionID {
				t.Fatal("native usage lost its original attribution", err)
			}
			sources[u.Usage.Source]++
			if u.Usage.Result != nil {
				result := u.Usage.Result
				if result.MainLoop == nil || result.MainLoop.Input == nil || *result.MainLoop.Input != "3" || result.MainLoop.Output == nil || *result.MainLoop.Output != "4" || result.NativeCostUSD == nil || result.Models["fixture-model"].Input == nil {
					t.Fatal("native result usage lost its independent scopes")
				}
			}
			if _, err := f.service.Store.ResponseUsage(ctx, r.ID); err == nil {
				t.Fatal("overlapping native reports entered billing ledger")
			}
		}
		if sources[domain.ClaudeMessageStartUsage] != 1 || sources[domain.ClaudeBlockCompleteUsage] != 4 || sources[domain.ClaudeMessageMetadataUsage] != 1 || sources[domain.ClaudeInputResultUsage] != 1 {
			t.Fatal("native usage scopes were merged")
		}
	}

	if calls.Load() != 1 {
		t.Fatal("registered native execution repeated provider inference")
	}
	if retained, err := security.ReadPrivate(filepath.Join(root, "claimed-input.json"), 1024); err != nil || string(retained) != string(claim) || strings.Contains(string(retained), f.token) {
		t.Fatal("original claim changed or contained credentials", err)
	}
}
