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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type claudePublicCase string

const (
	claudePublicBackgroundStop   claudePublicCase = "background-stop"
	claudePublicBashToolProgress claudePublicCase = "bash-tool-progress"
	claudePublicBashTask         claudePublicCase = "continuation-bash-task"
	claudePublicBash             claudePublicCase = "continuation-bash"
	claudePublicWrite            claudePublicCase = "continuation-write"
	claudePublicEdit             claudePublicCase = "continuation-edit"
)

const (
	claudePublicText                 claudePublicCase = "text"
	claudePublicContinuation         claudePublicCase = "continuation"
	claudePublicRetry                claudePublicCase = "retry"
	claudePublicQuestionRetry        claudePublicCase = "question-retry"
	claudePublicQuestion             claudePublicCase = "question"
	claudePublicQuestionInterrupt    claudePublicCase = "question-interrupt"
	claudePublicStop                 claudePublicCase = "stop"
	claudePublicArchive              claudePublicCase = "archive"
	claudePublicStopBeforeAcceptance claudePublicCase = "stop-before-acceptance"
	claudePublicResume               claudePublicCase = "continuation-resume"
	claudePublicCheckpointChanged    claudePublicCase = "continuation-checkpoint-changed"
	claudePublicHistoryChanged       claudePublicCase = "continuation-history-changed"
	claudePublicClaimChanged         claudePublicCase = "continuation-claim-changed"
	claudePublicReportChanged        claudePublicCase = "continuation-report-changed"
	claudePublicOutboxChanged        claudePublicCase = "continuation-outbox-changed"
	claudePublicRead                 claudePublicCase = "continuation-read"
	claudePublicReadError            claudePublicCase = "continuation-read-error"
	claudePublicQuestionHistory      claudePublicCase = "continuation-question"
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

func TestManualNativeClaudePublicInterruptedDenial(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, claudePublicQuestionInterrupt) })
	}
}

func TestManualNativeClaudePublicContinuation(t *testing.T) {
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) { nativeClaudePublicDispatch(t, mode, claudePublicContinuation) })
	}
}

func TestManualNativeClaudePublicContinuationEvidence(t *testing.T) {
	for _, scenario := range []claudePublicCase{claudePublicResume, claudePublicCheckpointChanged, claudePublicHistoryChanged, claudePublicClaimChanged, claudePublicReportChanged, claudePublicOutboxChanged} {
		t.Run(string(scenario), func(t *testing.T) { nativeClaudePublicDispatch(t, domain.ExecuteMode, scenario) })
	}
}

func nativeClaudePublicDispatch(t *testing.T, mode domain.SessionMode, scenario claudePublicCase, recovery ...claudePublicRecoveryCase) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_CLAUDE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned Claude binary and scripted provider required")
	}
	timeout := 45 * time.Second
	// The pinned native main-tool heartbeat is emitted every 30 seconds. Keep
	// the opt-in long-tool case alive through that original timer and cleanup.
	if scenario == claudePublicBashToolProgress {
		timeout = 70 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	turns, turn := 1, 0
	continuation := strings.HasPrefix(string(scenario), "continuation")
	read := scenario == claudePublicRead || scenario == claudePublicReadError
	effect := claudePublicEffectName(scenario)
	background := scenario == claudePublicBackgroundStop
	taskStarted := make(chan string, 1)
	taskPublished := false
	effectCalls := int32(2)
	if scenario == claudePublicEdit || background {
		effectCalls = 3
	}
	effectHistory := &claudePublicEffectHistory{}
	questionHistory := scenario == claudePublicQuestionHistory
	missingRead := scenario == claudePublicReadError
	fault := continuation && !read && !questionHistory && effect == "" && scenario != claudePublicContinuation && scenario != claudePublicResume
	var readRoot atomic.Value
	if continuation {
		turns = 3
	}
	if fault {
		turns = 2
	}
	var previous domain.ExecutionCompletion
	denied := scenario == claudePublicQuestionInterrupt
	question := questionHistory || scenario == claudePublicQuestion || scenario == claudePublicQuestionRetry || denied
	retrying := scenario == claudePublicRetry || scenario == claudePublicQuestionRetry
	streamStopping := scenario == claudePublicStop || scenario == claudePublicArchive
	stopping := streamStopping || scenario == claudePublicStopBeforeAcceptance
	var calls atomic.Int32
	providerEnded := make(chan struct{})
	expected := int32(turns)
	if read || questionHistory {
		expected *= 2
	}
	if question && !denied && !questionHistory {
		expected++
	}
	if effect != "" {
		expected *= effectCalls
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
		if read {
			if !checkClaudePublicReadHistory(t, raw, n, missingRead) {
				w.WriteHeader(400)
				return
			}
		} else if questionHistory {
			if !checkClaudePublicQuestionHistory(t, raw, n) {
				w.WriteHeader(400)
				return
			}
		} else if effect != "" && !background {
			if !effectHistory.check(t, raw, n, effect, effectCalls) {
				w.WriteHeader(400)
				return
			}
		} else if continuation {
			var request struct {
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
				} `json:"messages"`
			}
			if json.Unmarshal(raw, &request) != nil {
				t.Error("continuation lost original message count")
				w.WriteHeader(400)
				return
			}
			original := 0
			for _, message := range request.Messages {
				if message.Role == "system" {
					continue
				}
				i := original
				original++
				want, role := "Original public Claude result.", "assistant"
				if i%2 == 0 {
					role = "user"
					want = fmt.Sprintf("continuation input %d", i/2)
					if i == 0 {
						want = "first retained input"
					}
				}
				if message.Role != role || !strings.Contains(string(message.Content), want) {
					t.Error("continuation replaced or reordered original history")
					w.WriteHeader(400)
					return
				}
			}
			if original != int(2*n-1) {
				t.Error("continuation lost or duplicated ordinary history", n, original)
				w.WriteHeader(400)
				return
			}
		}
		if background && n == 3 && !claudePublicBackgroundResult(t, raw, "toolu_public_background_stop", "") {
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
		if background && n < 3 {
			name, id, params := "Bash", "toolu_public_background", map[string]any{"command": "sleep 60", "description": "Wait in the owned fixture scope", "run_in_background": true}
			if n == 2 {
				var task string
				select {
				case task = <-taskStarted:
				case <-ctx.Done():
					w.WriteHeader(400)
					return
				}
				if !claudePublicBackgroundResult(t, raw, "toolu_public_background", task) {
					w.WriteHeader(400)
					return
				}
				name, id, params = "TaskStop", "toolu_public_background_stop", map[string]any{"task_id": task}
			}
			encoded, _ := json.Marshal(params)
			block = map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(encoded)}
			reason = "tool_use"
		} else if effect != "" && n%effectCalls != 0 {
			name, id, params := claudePublicEffectCall(effect, n, effectCalls, readRoot.Load().(string))
			if scenario == claudePublicBashTask {
				params["command"] = "sleep 4; " + params["command"].(string)
			}
			if scenario == claudePublicBashToolProgress {
				params["command"] = "sleep 32; " + params["command"].(string)
			}
			encoded, _ := json.Marshal(params)
			block = map[string]any{"type": "tool_use", "id": id, "name": name, "input": map[string]any{}}
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(encoded)}
			reason = "tool_use"
		} else if read && n%2 == 1 {
			params, _ := json.Marshal(map[string]any{"file_path": filepath.Join(readRoot.Load().(string), fmt.Sprintf("original-%d.txt", (n-1)/2))})
			block = map[string]any{"type": "tool_use", "id": fmt.Sprintf("toolu_public_read_%d", (n-1)/2), "name": "Read", "input": map[string]any{}}
			delta = map[string]any{"type": "input_json_delta", "partial_json": string(params)}
			reason = "tool_use"
		} else if question && (n == 1 || questionHistory && n%2 == 1) {
			params, _ := json.Marshal(map[string]any{"questions": []any{map[string]any{"question": "Which original option?", "header": "Choice", "multiSelect": false, "options": []any{map[string]any{"label": "One", "description": "First option"}, map[string]any{"label": "Two", "description": "Second option"}}}}})
			toolID := "toolu_public_original"
			if questionHistory {
				toolID = fmt.Sprintf("toolu_public_question_%d", (n-1)/2)
			}
			block = map[string]any{"type": "tool_use", "id": toolID, "name": "AskUserQuestion", "input": map[string]any{}}
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
	if read || effect != "" {
		row, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(f.change.WorkspaceJob.Id))
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](row)
		var manifest workspace.Manifest
		if err != nil || domain.Decode(job.Output, &manifest) != nil || manifest.PrimaryPath == "" {
			t.Fatal("original fixture workspace unavailable", err)
		}
		readRoot.Store(manifest.PrimaryPath)
		for i := 0; i < turns; i++ {
			if !missingRead && (effect == "" || effect == "Edit") {
				if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, fmt.Sprintf("original-%d.txt", i)), []byte(fmt.Sprintf("Original retained Read %d.\n", i)), 0600); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	endpoint := f.endpoint.URL
	var recoveryFixture *claudePublicRecoveryFixture
	if len(recovery) != 0 {
		perTurn := int32(1)
		if read || questionHistory {
			perTurn = 2
		}
		if effect != "" {
			perTurn = effectCalls
		}
		recoveryFixture = newClaudePublicRecoveryFixture(t, f, &calls, recovery[0], perTurn)
		endpoint = recoveryFixture.endpoint
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: endpoint, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	stop := startClaudePublicFixtureWorker(t, ctx, f.workerRoot)
	defer func() { stop() }()
	for next := 1; next < turns; next++ {
		body, _ := json.Marshal(domain.SessionInput{Prompt: fmt.Sprintf("continuation input %d", next), Mode: mode})
		if _, err := sessionClient(f.accountFixture).EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: body})); err != nil {
			t.Fatal(err)
		}
	}
	sr := f.refresh(t)
	response, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil || response.Msg.Change.ExecutionJob == nil {
		t.Fatal("public first dispatch failed", err)
	}
	assignment := response.Msg.Change.ExecutionJob
	interactionClient := delidevv1connect.NewInteractionServiceClient(http.DefaultClient, f.endpoint.URL)
	answered, stopSent := false, false
	recoveredBoundary := false
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
		if background && !taskPublished {
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			if session.Execution != nil && session.Execution.ClaudeTasks != nil {
				for id, task := range session.Execution.ClaudeTasks.Tasks {
					if task.Tool.NativeID != "toolu_public_background" || task.Status.Terminal() {
						t.Fatal("background task owner changed before stop")
					}
					taskStarted <- id
					taskPublished = true
				}
			}
		}
		if (question || effect != "") && !answered {
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 10})
			if err != nil {
				t.Fatal(err)
			}
			current := rows[:0]
			for _, row := range rows {
				value, err := store.Decode[domain.ExecutionInteraction](row)
				if err != nil {
					t.Fatal(err)
				}
				if session.Execution != nil && value.ExecutionID == session.Execution.ExecutionID {
					current = append(current, row)
				}
			}
			rows = current
			if len(rows) > 1 {
				t.Fatal("original public question repeated")
			}
			if len(rows) == 1 && effect != "" {
				body, _ := json.Marshal(domain.ApprovalResponseInput{Claude: &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow}})
				req := &pb.RespondApprovalRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(rows[0].ID), ExpectedRevision: rows[0].Revision}, ResponseJson: body}
				if _, err := interactionClient.RespondApproval(ctx, ownerRequest(f.identity, req)); err != nil {
					t.Fatal("original tool approval", err)
				}
				if replay, err := interactionClient.RespondApproval(ctx, ownerRequest(f.identity, req)); err != nil || !replay.Msg.Replayed {
					t.Fatal("original approval receipt", err)
				}
				answered = true
			} else if len(rows) == 1 {
				reply := &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyAllow, Answers: map[string]string{"Which original option?": "Two"}}
				if denied {
					message, interrupt := "Original public interrupted denial", true
					reply = &domain.ClaudePermissionResponse{Behavior: domain.ClaudeReplyDeny, Message: &message, Interrupt: &interrupt}
				}
				body, _ := json.Marshal(domain.QuestionResponseInput{Claude: reply})
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
		if recoveryFixture != nil && recoveryFixture.ready() {
			stop()
			stop = recoveryFixture.replaceWorker(t, ctx, f)
			if !recoveryFixture.reconcile(t, ctx, f, assignment) {
				return
			}
			recoveredBoundary = true
			record, err = f.service.Store.Get(ctx, domain.JobKind, domain.ID(assignment.Id))
			if err != nil {
				t.Fatal(err)
			}
			job, err = store.Decode[domain.Job](record)
			if err != nil {
				t.Fatal(err)
			}
		}
		if job.State.Terminal() || job.State == domain.JobUncertain {
			if fault && turn > 0 {
				session, err := store.Decode[domain.Session](f.refresh(t))
				if err != nil || job.State != domain.JobUncertain || len(job.Output) != 0 || calls.Load() != 1 || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused {
					t.Fatal("changed history acquired another input", job.State, job.Problem, err)
				}
				return
			}
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
			if denied {
				session, err := store.Decode[domain.Session](f.refresh(t))
				var proof domain.ExecutionCompletion
				if err != nil || job.State != domain.JobCanceled || domain.Decode(job.Output, &proof) != nil || proof.ValidateForHarness(domain.ClaudeCode) != nil || proof.Outcome != domain.ExecutionStopped || proof.Version != 1 || calls.Load() != 1 || !answered || session.Execution == nil || session.Execution.ClaudeDenial == nil || session.Execution.ClaudeDenial.Validate() != nil || session.Execution.ClaudeTerminal != nil || session.Execution.ClaudeStop != nil || !session.Execution.CleanupVerified || session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID == "" || session.Outcome != domain.ExecutionStopped {
					t.Fatal("original denial lost independent cleanup or recovery", job.State, job.Problem, err)
				}
				return
			}
			var proof domain.ExecutionCompletion
			version, dispatch := uint32(2), domain.DispatchReady
			if background {
				version, dispatch = 1, domain.DispatchPaused
			}
			if recoveredBoundary {
				dispatch = domain.DispatchPaused
			}
			wantCalls := expected
			if turns > 1 {
				wantCalls = int32(turn + 1)
				if read || questionHistory {
					wantCalls *= 2
				}
				if effect != "" {
					wantCalls *= effectCalls
				}
			}
			if job.State != domain.JobSucceeded || domain.Decode(job.Output, &proof) != nil || proof.ValidateForHarness(domain.ClaudeCode) != nil || proof.Version != version || calls.Load() != wantCalls || !background && (question || effect != "") && !answered {
				t.Fatalf("public Claude execution did not finish: %s %v", job.State, job.Problem)
			}
			session, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || session.Execution == nil || session.Execution.ClaudeTerminal == nil || !session.Execution.CleanupVerified || session.Dispatch != dispatch || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || session.PendingInputs != uint32(turns-turn-1) || session.Outcome != domain.ExecutionSucceeded {
				t.Fatal("public completion lost original outcome, cleanup or history readiness", err)
			}
			if !background && (question || effect != "") {
				verifyClaudePublicQuestionResults(t, ctx, f, turn+1)
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
			if scenario == claudePublicBashTask || scenario == claudePublicBashToolProgress {
				verifyClaudePublicBashTask(t, ctx, f, proof, scenario == claudePublicBashToolProgress)
			}
			if background {
				verifyClaudePublicBackgroundStop(t, ctx, f, proof)
			}
			if effect != "" && !background {
				verifyClaudePublicEffectFiles(t, readRoot.Load().(string), turn)
			}
			if read {
				verifyClaudePublicReadResult(t, ctx, f, proof, turn, missingRead)
			}
			if turn > 0 && (proof.NativeThreadID != previous.NativeThreadID || proof.NativeTurnID == previous.NativeTurnID || proof.ExecutionID == previous.ExecutionID || proof.NativeCheckpointDigest == previous.NativeCheckpointDigest) {
				t.Fatal("continuation reused authority or replaced native session")
			}
			previous = proof
			turn++
			answered = false
			if turn < turns {
				if fault {
					alterClaudeContinuationEvidence(t, ctx, f.workerRoot, domain.ID(assignment.Id), proof, scenario)
				}
				if scenario == claudePublicResume && turn == 1 || recoveredBoundary {
					actions := []pb.SessionAction{pb.SessionAction_SESSION_ACTION_RESUME}
					if !recoveredBoundary {
						actions = append([]pb.SessionAction{pb.SessionAction_SESSION_ACTION_STOP}, actions...)
					}
					for _, action := range actions {
						current := f.refresh(t)
						if _, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(current.ID), ExpectedRevision: current.Revision}, Action: action})); err != nil {
							t.Fatal("explicit original-history Resume", err)
						}
					}
				} else if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
					t.Fatal("automatic original-history continuation", err)
				}
				var next store.Record
				if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
					_, current, err := sessionRecord(tx, sr.ID)
					if err != nil {
						return err
					}
					next, err = tx.SessionExecutionJob(sr.ID, current.ActiveExecutionID)
					return err
				}); err != nil {
					t.Fatal(err)
				}
				assignment = &pb.Resource{Id: string(next.ID), DocumentJson: next.Data}
				var nextJob domain.Job
				var nextInput domain.ExecutionJobInput
				if domain.Decode(next.Data, &nextJob) != nil || domain.Decode(nextJob.Input, &nextInput) != nil || nextInput.Validate() != nil || nextInput.Continuation == nil || nextInput.Continuation.Completion != previous || nextInput.Input.Mode != mode {
					t.Fatal("continuation lost exact original predecessor")
				}
				recoveredBoundary = false
				continue
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

// Wait only for the accepted report's local acknowledgment transition before
// altering disposable evidence; otherwise the writer could repair the fixture.
func alterClaudeContinuationEvidence(t *testing.T, ctx context.Context, root string, job domain.ID, proof domain.ExecutionCompletion, scenario claudePublicCase) {
	t.Helper()
	report := filepath.Join(root, "jobs", string(job)+".json")
	for {
		raw, err := os.ReadFile(report)
		var operation struct {
			State string `json:"state"`
		}
		if err == nil && json.Unmarshal(raw, &operation) == nil && operation.State == "reported" {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("original report did not finish")
		}
	}
	path := filepath.Join(root, "runtimes", string(proof.ExecutionID), "native-completion.json")
	switch scenario {
	case claudePublicHistoryChanged:
		path = ""
		err := filepath.WalkDir(filepath.Join(root, "runtimes", string(proof.ExecutionID), "claude"), func(candidate string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Name() == string(proof.NativeThreadID)+".jsonl" {
				if path != "" {
					t.Fatal("ambiguous original history")
				}
				path = candidate
			}
			return nil
		})
		if err != nil || path == "" {
			t.Fatal("original history unavailable", err)
		}
	case claudePublicClaimChanged:
		path = filepath.Join(root, "jobs", string(job), "claude-claims.json")
	case claudePublicReportChanged:
		path = report
	case claudePublicOutboxChanged:
		path = filepath.Join(root, "jobs", string(job), "publication.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if scenario == claudePublicClaimChanged || scenario == claudePublicReportChanged || scenario == claudePublicOutboxChanged {
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid original fixture journal")
		}
		switch scenario {
		case claudePublicClaimChanged:
			value["input_claimed"] = json.RawMessage(`false`)
		case claudePublicReportChanged:
			value["state"] = json.RawMessage(`"started"`)
		case claudePublicOutboxChanged:
			value["last_sequence"] = json.RawMessage(`0`)
		}
		raw, _ = json.Marshal(value)
	} else if scenario == claudePublicHistoryChanged {
		raw = append(raw, []byte("{}\n")...)
	} else {
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
