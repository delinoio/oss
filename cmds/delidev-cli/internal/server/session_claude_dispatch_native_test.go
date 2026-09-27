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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type claudePublicCase string

const (
	claudePublicText                 claudePublicCase = "text"
	claudePublicRetry                claudePublicCase = "retry"
	claudePublicQuestionRetry        claudePublicCase = "question-retry"
	claudePublicQuestion             claudePublicCase = "question"
	claudePublicStop                 claudePublicCase = "stop"
	claudePublicArchive              claudePublicCase = "archive"
	claudePublicStopBeforeAcceptance claudePublicCase = "stop-before-acceptance"
)

func TestManualNativeClaudePublicCancellationContainment(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []claudePublicCase{claudePublicStopBeforeAcceptance, claudePublicStop, claudePublicArchive} {
			t.Run(fmt.Sprintf("%s/%s", mode, scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, scenario) })
		}
	}
}

func TestManualNativeClaudePublicFirstDispatch(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []claudePublicCase{claudePublicText, claudePublicQuestion} {
			t.Run(fmt.Sprintf("%s/%s", mode, scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, scenario) })
		}
	}
}

func TestManualNativeClaudePublicRetry(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []claudePublicCase{claudePublicRetry, claudePublicQuestionRetry} {
			t.Run(fmt.Sprintf("%s/%s", mode, scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, scenario) })
		}
	}
}

func nativeClaudePublicDispatch(t *testing.T, mode domain.SessionMode, scenario claudePublicCase) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned Claude binary and scripted provider required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	question := scenario == claudePublicQuestion || scenario == claudePublicQuestionRetry
	retrying := scenario == claudePublicRetry || scenario == claudePublicQuestionRetry
	streamStopping := scenario == claudePublicStop || scenario == claudePublicArchive
	stopping := streamStopping || scenario == claudePublicStopBeforeAcceptance
	var calls atomic.Int32
	providerEnded := make(chan struct{})
	expected := int32(1)
	if question {
		expected++
	}
	if retrying {
		expected++
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
			return
		}
		n := calls.Add(1)
		if n > expected || r.Method != http.MethodPost || r.URL.Path != "/messages" || r.URL.RawQuery != "beta=true" || r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
			t.Error("public Claude request escaped selected keyless relay")
			w.WriteHeader(400)
			return
		}
		raw, _ := io.ReadAll(io.LimitReader(r.Body, 8<<20))
		if !strings.Contains(string(raw), "first retained input") || question && n >= 2 && !strings.Contains(string(raw), "Two") {
			t.Error("original public input or answer changed")
			w.WriteHeader(400)
			return
		}
		if retrying && (question && n == 2 || !question && n == 1) {
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"overloaded_error","message":"Scripted retry"}}`)
			return
		}
		if scenario == claudePublicStopBeforeAcceptance {
			defer close(providerEnded)
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
			}
			return
		}
		block := map[string]any{"type": "text", "text": ""}
		delta := map[string]any{"type": "text_delta", "text": "Original public Claude result."}
		reason := "end_turn"
		if question && n == 1 {
			params, _ := json.Marshal(map[string]any{"questions": []any{map[string]any{"question": "Which original option?", "header": "Choice", "multiSelect": false, "options": []any{map[string]any{"label": "One", "description": "First option"}, map[string]any{"label": "Two", "description": "Second option"}}}}})
			block = map[string]any{"type": "tool_use", "id": "toolu_public_original", "name": "AskUserQuestion", "input": map[string]any{}}
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(params)}
			reason = "tool_use"
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for i, event := range []map[string]any{
			{"type": "message_start", "message": map[string]any{"id": fmt.Sprintf("msg_public_%d", n), "type": "message", "role": "assistant", "content": []any{}, "model": "fixture-model", "stop_reason": nil, "stop_sequence": nil, "usage": map[string]any{"input_tokens": 1, "output_tokens": 0}}},
			{"type": "content_block_start", "index": 0, "content_block": block}, {"type": "content_block_delta", "index": 0, "delta": delta}, {"type": "content_block_stop", "index": 0}, {"type": "message_delta", "delta": map[string]any{"stop_reason": reason, "stop_sequence": nil}, "usage": map[string]any{"output_tokens": 2}}, {"type": "message_stop"},
		} {
			encoded, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event["type"], encoded)
			if streamStopping && i == 2 {
				// Native user replay follows the provider's first streamed bytes.
				// Keep the original content unfinished while public Stop arrives.
				w.(http.Flusher).Flush()
				defer close(providerEnded)
				select {
				case <-r.Context().Done():
				case <-ctx.Done():
				}
				return
			}
		}
	}))
	defer upstream.Close()
	f := newFirstDispatchFixtureProfile(t, domain.ClaudeCode, mode, binary, upstream.URL)
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.endpoint.URL, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	running, stop := context.WithCancel(ctx)
	done, ready := make(chan error, 1), make(chan domain.ID, 1)
	go func() {
		done <- worker.Run(running, worker.Config{Root: f.workerRoot, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), Ready: func(id domain.ID) { ready <- id }})
	}()
	defer func() { stop(); <-done }()
	select {
	case <-ready:
	case <-ctx.Done():
		t.Fatal("original Worker did not attach")
	}
	sr := f.refresh(t)
	response, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil || response.Msg.Change.ExecutionJob == nil {
		t.Fatal("public first dispatch failed", err)
	}
	assignment := response.Msg.Change.ExecutionJob
	interactionClient := delidevv1connect.NewInteractionServiceClient(http.DefaultClient, f.endpoint.URL)
	answered, stopSent := false, false
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		if stopping && !stopSent && calls.Load() == 1 {
			r := f.refresh(t)
			session, err := store.Decode[domain.Session](r)
			if err != nil {
				t.Fatal(err)
			}
			ready := scenario == claudePublicStopBeforeAcceptance
			if !ready {
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 50})
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					message, err := store.Decode[domain.ExecutionMessage](row)
					if err == nil && message.Claude != nil && len(message.Claude.Blocks) == 1 && message.Claude.Blocks[0].Block.Text == "Original public Claude result." {
						ready = true
					}
				}
			}
			if session.Execution != nil && ready {
				action := pb.SessionAction_SESSION_ACTION_STOP
				if scenario == claudePublicArchive {
					action = pb.SessionAction_SESSION_ACTION_ARCHIVE
				}
				_, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
				if err != nil && connect.CodeOf(err) != connect.CodeAborted {
					t.Fatal("original public Stop", err)
				}
				stopSent = err == nil
			}
		}
		if question && !answered {
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) > 1 {
				t.Fatal("original public question repeated")
			}
			if len(rows) == 1 {
				body, _ := json.Marshal(domain.QuestionResponseInput{Claude: &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow, Answers: map[string]string{"Which original option?": "Two"}}})
				req := &pb.RespondQuestionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(rows[0].ID), ExpectedRevision: rows[0].Revision}, ResponseJson: body}
				if _, err := interactionClient.RespondQuestion(ctx, ownerRequest(f.identity, req)); err != nil {
					t.Fatal("public question response", err)
				}
				if replay, err := interactionClient.RespondQuestion(ctx, ownerRequest(f.identity, req)); err != nil || !replay.Msg.Replayed {
					t.Fatal("public question response receipt", err)
				}
				answered = true
			}
		}
		record, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(assignment.Id))
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			t.Fatal(err)
		}
		if job.State.Terminal() || job.State == domain.JobUncertain {
			if stopping {
				session, err := store.Decode[domain.Session](f.refresh(t))
				if err != nil || !stopSent || calls.Load() != 1 || session.Dispatch != domain.DispatchPaused || session.Execution == nil || session.Execution.ClaudeTerminal != nil {
					t.Fatal("Stop lost original ownership", err)
				}
				if streamStopping {
					if (session.Archive == domain.Archived) != (scenario == claudePublicArchive) {
						t.Fatal("original archive cleanup did not settle")
					}
					var proof domain.ExecutionCompletion
					if job.State != domain.JobCanceled || domain.Decode(job.Output, &proof) != nil || proof.ValidateForHarness(domain.ClaudeCode) != nil || proof.Outcome != domain.ExecutionStopped || proof.Version != 1 || session.Execution.ClaudeStop == nil || session.Execution.ClaudeStop.Validate() != nil || !session.Execution.CleanupVerified || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" {
						t.Fatal("original streaming Stop did not settle", job.State, job.Problem)
					}
				} else if job.State != domain.JobUncertain || len(job.Output) != 0 || session.Recovery != domain.NeedsRecovery || session.Execution.CleanupVerified || session.Execution.ClaudeStop != nil {
					t.Fatal("Stop invented native input completion or lost original uncertainty", err, job.State, job.Problem)
				}
				select {
				case <-providerEnded:
				case <-ctx.Done():
					t.Fatal("original inference survived Stop")
				}
				return
			}
			var proof domain.ExecutionCompletion
			if job.State != domain.JobSucceeded || domain.Decode(job.Output, &proof) != nil || proof.ValidateForHarness(domain.ClaudeCode) != nil || proof.Version != 1 || calls.Load() != expected || question && !answered {
				t.Fatalf("public Claude execution did not finish: %s %v", job.State, job.Problem)
			}
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || session.Execution == nil || session.Execution.ClaudeTerminal == nil || !session.Execution.CleanupVerified || session.Dispatch != domain.DispatchPaused || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || session.PendingInputs != 0 || session.Outcome != domain.ExecutionSucceeded {
				t.Fatal("public completion lost original outcome/cleanup or enabled continuation", err)
			}
			if question {
				rows, _ := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 10})
				v, err := store.Decode[domain.ExecutionInteraction](rows[0])
				if err != nil || v.ClaudeSettlement == nil || v.Response == nil || v.Response.State != domain.QuestionResponseAccepted {
					t.Fatal("public callback did not settle", err)
				}
			}
			if retrying {
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: domain.ID(f.change.Session.Id), Limit: 50})
				if err != nil {
					t.Fatal(err)
				}
				retries, inputs := 0, 0
				for _, row := range rows {
					m, err := store.Decode[domain.ExecutionMessage](row)
					if err != nil {
						t.Fatal(err)
					}
					if m.InputID == session.Execution.InputID {
						inputs++
					}
					if p := m.ClaudeProgress; p != nil && p.Kind == domain.ClaudeAPIRetryProgress {
						retries++
						if p.Validate() != nil || p.InputAccepted != question || p.APIRetry.ErrorStatus == nil || *p.APIRetry.ErrorStatus != 503 || session.Execution.ClaudeProgress == nil || session.Execution.ClaudeProgress.LatestRetryID != row.ID {
							t.Fatal("original retry lost exact status or retained reference")
						}
						t.Logf("original retry: accepted=%t attempt=%s maximum=%s delay_ms=%s error=%s", p.InputAccepted, p.APIRetry.Attempt, p.APIRetry.MaxRetries, p.APIRetry.DelayMS, p.APIRetry.Error)
					}
				}
				if retries != 1 || inputs != 1 {
					t.Fatal("native retry duplicated input or lost progress", retries, inputs)
				}
			}
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("public Claude execution did not finish")
		}
	}
}
