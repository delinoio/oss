package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type nativeWorkerScenario uint8

const (
	nativeWorkerCompletion nativeWorkerScenario = iota
	nativeWorkerCommand
	nativeWorkerPlan
	nativeWorkerRevocation
	nativeWorkerDisconnect
	nativeWorkerStop
	nativeWorkerArchive
	nativeWorkerQuestionStop
	nativeWorkerQuestionResponse
	nativeWorkerApprovalStop
	nativeWorkerApprovalResponse
)

func TestManualNativeWorkerExecutesAcceptedCodexJob(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerCompletion)
}

func TestManualNativeWorkerPublishesCodexCommand(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this installed-harness fixture uses a POSIX shell builtin")
	}
	testManualNativeWorkerExecution(t, nativeWorkerCommand)
}

func TestManualNativeWorkerPublishesCodexPlanProgress(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerPlan)
}

func TestManualNativeWorkerRetainsQuestionUntilStop(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerQuestionStop)
}

func TestManualNativeWorkerRetainsApprovalUntilStop(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("this installed-harness fixture uses a POSIX shell builtin")
	}
	testManualNativeWorkerExecution(t, nativeWorkerApprovalStop)
}

func TestManualNativeWorkerDeliversOwnedApprovalResponse(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerApprovalResponse)
}

func TestManualNativeWorkerDeliversOwnedQuestionResponse(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerQuestionResponse)
}

func TestManualNativeWorkerRevocationJoinsCodexCleanup(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerRevocation)
}

func TestManualNativeWorkerAccountDisconnectCancelsCodex(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerDisconnect)
}

func TestManualNativeWorkerStopInterruptsCodex(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerStop)
}

func TestManualNativeWorkerArchiveWaitsForCodexCleanup(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerArchive)
}

func testManualNativeWorkerExecution(t *testing.T, scenario nativeWorkerScenario) {
	t.Helper()
	binary := os.Getenv("DELIDEV_NATIVE_THREAD_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed Codex, private Worker and scripted loopback provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be explicitly absolute")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	toolScenario := scenario == nativeWorkerCommand || scenario == nativeWorkerPlan
	approvalResponseScenario := scenario == nativeWorkerApprovalResponse
	responseScenario := scenario == nativeWorkerQuestionResponse || approvalResponseScenario
	questionScenario := scenario == nativeWorkerQuestionStop || scenario == nativeWorkerQuestionResponse
	approvalScenario := scenario == nativeWorkerApprovalStop || approvalResponseScenario
	interactionScenario := questionScenario || approvalScenario
	completedScenario := scenario == nativeWorkerCompletion || toolScenario || responseScenario
	var calls atomic.Int64
	started, upstreamStopped := make(chan struct{}, 1), make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" {
			t.Error("Worker inference escaped server-only API authority")
			http.Error(w, "unsupported", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !strings.Contains(string(body), "Fixture prompt") || !strings.Contains(string(body), "fixture-model") {
			t.Error("Worker changed the accepted native input/model")
		}
		if !completedScenario && !interactionScenario {
			started <- struct{}{}
			<-r.Context().Done()
			upstreamStopped <- struct{}{}
			return
		}
		if (toolScenario || interactionScenario) && call == 1 {
			toolName := "exec_command"
			arguments := map[string]any{"cmd": "printf 'native-tool-fixture\\n'", "login": false, "max_output_tokens": 1000, "yield_time_ms": 1000}
			if scenario == nativeWorkerPlan {
				toolName = "update_plan"
				arguments = map[string]any{"explanation": "native plan fixture", "plan": []any{map[string]any{"step": "Retain native plan progress", "status": "completed"}}}
			}
			if questionScenario {
				started <- struct{}{}
				toolName = "request_user_input"
				arguments = map[string]any{"questions": []any{map[string]any{"id": "choice", "header": "Choose", "question": "Native Worker question", "options": []any{map[string]any{"label": "First", "description": "First option"}, map[string]any{"label": "Second", "description": "Second option"}}}}}
			}
			if approvalScenario {
				started <- struct{}{}
				arguments["sandbox_permissions"], arguments["justification"] = "require_escalated", "Private native Worker approval fixture"
			}
			var toolRequest struct {
				Tools []struct{ Type, Name string } `json:"tools"`
			}
			if json.Unmarshal(body, &toolRequest) != nil {
				t.Error("native tool definitions unavailable")
			}
			found := false
			for _, tool := range toolRequest.Tools {
				found = found || (tool.Type == "function" && tool.Name == toolName)
			}
			if !found {
				t.Error("selected native profile did not expose the required tool")
			}
			args, _ := json.Marshal(arguments)
			w.Header().Set("Content-Type", "text/event-stream")
			for _, event := range []any{
				map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_worker_tool", "status": "in_progress"}},
				map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "function_call", "id": "fc_worker_fixture", "call_id": "call_worker_fixture", "name": toolName, "arguments": string(args)}},
				map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_worker_tool", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
			} {
				raw, _ := json.Marshal(event)
				_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
			}
			return
		}
		if toolScenario && (call != 2 || !strings.Contains(string(body), "function_call_output") || !strings.Contains(string(body), "call_worker_fixture") || (scenario == nativeWorkerCommand && !strings.Contains(string(body), "native-tool-fixture"))) {
			t.Error("native command output did not reach the same selected account")
		}
		if interactionScenario && !responseScenario {
			t.Error("unanswered native interaction triggered another model request")
		}
		if approvalResponseScenario && (call != 2 || !strings.Contains(string(body), "function_call_output") || !strings.Contains(string(body), "native-tool-fixture")) {
			t.Error("approved native command did not return actual output")
		}
		if responseScenario && !approvalResponseScenario {
			var request struct {
				Input []struct {
					Type   string          `json:"type"`
					CallID string          `json:"call_id"`
					Output json.RawMessage `json:"output"`
				} `json:"input"`
			}
			matched := false
			if json.Unmarshal(body, &request) == nil && call == 2 {
				for _, item := range request.Input {
					if item.Type != "function_call_output" || item.CallID != "call_worker_fixture" {
						continue
					}
					var text string
					var answer struct {
						Answers map[string]struct {
							Answers []string `json:"answers"`
						} `json:"answers"`
					}
					if json.Unmarshal(item.Output, &text) == nil && domain.Decode([]byte(text), &answer) == nil && len(answer.Answers) == 1 && len(answer.Answers["choice"].Answers) == 1 && answer.Answers["choice"].Answers[0] == "Second" {
						matched = true
					}
				}
			}
			if !matched {
				t.Error("Worker question reply did not reach native core with the exact accepted answer")
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, event := range []any{
			map[string]any{"type": "response.created", "response": map[string]any{"id": "resp_worker_fixture", "status": "in_progress"}},
			map[string]any{"type": "response.output_item.done", "output_index": 0, "item": map[string]any{"type": "message", "id": "msg_worker_fixture", "role": "assistant", "content": []any{map[string]any{"type": "output_text", "text": "Worker fixture complete."}}}},
			map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_worker_fixture", "status": "completed", "output": []any{}, "usage": map[string]any{"input_tokens": 1, "output_tokens": 1, "total_tokens": 2}}},
		} {
			raw, _ := json.Marshal(event)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
	}))
	defer upstream.Close()
	manager := &workspace.Manager{Root: filepath.Join(t.TempDir(), "worker")}
	f := publicationFixtureFromAuthority(t, newConfiguredAuthorityFixture(t, upstream.URL, func(input *domain.ExecutionJobInput) {
		if questionScenario {
			input.Input.Mode = domain.PlanMode
		}
		preparation := workspace.PrepareRequest{SessionID: input.SessionID, MachineID: input.MachineID, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}}
		manifest, err := manager.Prepare(ctx, preparation)
		if err != nil {
			t.Fatal(err)
		}
		input.Preparation, _ = json.Marshal(preparation)
		input.Manifest, _ = json.Marshal(manifest)
		input.Installation.ResolvedPath = binary
	}, true))
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: f.http.URL, ServerID: f.service.Identity.ServerID, DeviceID: f.device, MachineID: f.input.MachineID, PairingID: domain.NewID(), Token: f.workerToken}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(manager.Root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	running, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	var workerErr error
	go func() {
		defer close(done)
		workerErr = worker.Run(running, worker.Config{Root: manager.Root, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil))})
	}()
	t.Cleanup(func() { stopWorker(); <-done })
	responseAccepted := make(chan error, 1)
	if responseScenario {
		responderDone := make(chan struct{})
		t.Cleanup(func() { cancel(); <-responderDone })
		// Exercise the public owner API while the real Worker receives, claims
		// and sends the active execution's durable response control.
		go func() {
			defer close(responderDone)
			inbox := delidevv1connect.NewInboxServiceClient(f.http.Client(), f.http.URL)
			for {
				changed := f.service.Store.Changed()
				rows, err := inbox.ListInbox(ctx, ownerRequest(f.service.Identity, &pb.ListInboxRequest{SessionId: string(f.input.SessionID), Source: pb.InboxSource_INBOX_SOURCE_INTERACTION, ReadState: pb.InboxReadState_INBOX_READ_STATE_UNREAD, PageSize: 2}))
				if err != nil {
					responseAccepted <- err
					return
				}
				if len(rows.Msg.Entries) == 1 {
					view := rows.Msg.Entries[0]
					if _, err := inbox.SetInboxReadState(ctx, ownerRequest(f.service.Identity, &pb.SetInboxReadStateRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: view.Entry.Id, ExpectedRevision: view.Entry.Revision}, ReadState: pb.InboxReadState_INBOX_READ_STATE_READ})); err != nil {
						responseAccepted <- err
						return
					}
					client := delidevv1connect.NewInteractionServiceClient(f.http.Client(), f.http.URL)
					mutation := &pb.Mutation{RequestId: string(domain.NewID()), Id: view.Interaction.Id, ExpectedRevision: view.Interaction.Revision}
					if approvalResponseScenario {
						raw, _ := json.Marshal(approvalInput())
						_, err = client.RespondApproval(ctx, ownerRequest(f.service.Identity, &pb.RespondApprovalRequest{Mutation: mutation, ResponseJson: raw}))
					} else {
						raw, _ := json.Marshal(domain.QuestionResponseInput{Answers: map[string][]string{"choice": {"Second"}}})
						_, err = client.RespondQuestion(ctx, ownerRequest(f.service.Identity, &pb.RespondQuestionRequest{Mutation: mutation, ResponseJson: raw}))
					}
					responseAccepted <- err
					return
				}
				select {
				case <-changed:
				case <-ctx.Done():
					responseAccepted <- ctx.Err()
					return
				}
			}
		}()
	}
	if !completedScenario {
	startedWait:
		for {
			changed := f.service.Store.Changed()
			r, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](r)
			if err != nil {
				t.Fatal(err)
			}
			if job.State == domain.JobUncertain || job.State.Terminal() {
				t.Fatalf("native Worker ended before its first inference: %s %v", job.State, job.Problem)
			}
			select {
			case <-started:
				break startedWait
			case <-changed:
			case <-ctx.Done():
				t.Fatal("native Worker did not reach its owned inference request")
			}
		}
		// The completed native user message proves the Worker crossed its
		// acceptance/publication boundary before testing graceful interruption.
		for scenario != nativeWorkerRevocation {
			changed := f.service.Store.Changed()
			r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](r)
			if err != nil {
				t.Fatal(err)
			}
			if session.Execution != nil && session.Execution.LastSequence >= 4 {
				if !interactionScenario {
					break
				}
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 1 && (session.Execution.Waiting.UserInput || (approvalScenario && session.Execution.Waiting.Approval)) {
					interaction, err := store.Decode[domain.ExecutionInteraction](rows[0])
					if approvalScenario {
						if err != nil || interaction.Closure != domain.InteractionOpen || interaction.Type != domain.NativeApprovalInteraction || interaction.Approval == nil || interaction.Approval.Codex.Command == nil || interaction.Approval.Codex.Command.Command == nil || !strings.Contains(*interaction.Approval.Codex.Command.Command, "native-tool-fixture") || len(interaction.Approval.Codex.Command.AvailableDecisions) == 0 || interaction.Response != nil {
							t.Fatal("native Worker lost original pending approval")
						}
						_, entry := readExecutionInbox(t, f, domain.InteractionInbox, rows[0].ID)
						if entry.ReadState != domain.InboxUnread {
							t.Fatal("native approval did not create unread inbox state")
						}
					} else if err != nil || interaction.Closure != domain.InteractionOpen || interaction.NativeItemID != "call_worker_fixture" || interaction.Type != domain.UserQuestionInteraction || !interaction.Questions.Blocking || len(interaction.Questions.Questions) != 1 || interaction.Questions.Questions[0].Text != "Native Worker question" {
						t.Fatal("native Worker did not retain its exact pending question")
					}
					break
				}
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatal("native input was not durably published before interruption")
			}
		}
		if scenario == nativeWorkerRevocation {
			devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
			_, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.device), ExpectedRevision: 1}}))
			if err != nil {
				t.Fatal(err)
			}
		} else if scenario == nativeWorkerDisconnect {
			accounts := delidevv1connect.NewAccountServiceClient(f.http.Client(), f.http.URL)
			response, err := accounts.DisconnectAccount(ctx, ownerRequest(f.service.Identity, &pb.DisconnectAccountRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.input.AccountID), ExpectedRevision: 1}}))
			if err != nil || len(response.Msg.CleanupProblemJson) != 0 {
				t.Fatalf("account disconnect failed: %v", err)
			}
		} else {
			r, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			sessions := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			action := pb.SessionAction_SESSION_ACTION_STOP
			if scenario == nativeWorkerArchive {
				action = pb.SessionAction_SESSION_ACTION_ARCHIVE
			}
			_, err = sessions.ControlSession(ctx, ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}))
			if err != nil {
				t.Fatal(err)
			}
		}
		if scenario != nativeWorkerRevocation {
			for {
				changed := f.service.Store.Changed()
				r, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
				if err != nil {
					t.Fatal(err)
				}
				job, err := store.Decode[domain.Job](r)
				if err != nil {
					t.Fatal(err)
				}
				if job.State == domain.JobUncertain || job.State.Terminal() {
					break
				}
				select {
				case <-changed:
				case <-ctx.Done():
					t.Fatal("account disconnect did not cancel and report the native job")
				}
			}
			stopWorker()
		}
		select {
		case <-done:
		case <-ctx.Done():
			t.Fatal("revoked Worker left its native operation running")
		}
		if scenario == nativeWorkerRevocation && (workerErr == nil || (domain.SafeError(workerErr).Code != domain.Unauthenticated && domain.SafeError(workerErr).Code != domain.PermissionDenied)) {
			t.Fatalf("Worker revocation lost its authority failure: %v", workerErr)
		}
		if !interactionScenario {
			select {
			case <-upstreamStopped:
			case <-ctx.Done():
				t.Fatal("revocation retained the native inference request")
			}
		}
		if calls.Load() != 1 {
			t.Fatal("revocation replayed native inference")
		}
		if err := process.ReconcileOwner(filepath.Join(manager.Root, "processes"), f.job); err != nil {
			t.Fatal("revoked Worker did not retain proof of native cleanup")
		}
		raw, err := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job)+".json"), 2<<20)
		var journal struct {
			State   string          `json:"state"`
			Output  json.RawMessage `json:"output"`
			Problem *domain.Error   `json:"problem"`
		}
		if err != nil || json.Unmarshal(raw, &journal) != nil || (journal.State != "finished" && journal.State != "reported") || (journal.Problem == nil) == (len(journal.Output) == 0) {
			t.Fatal("revoked Worker did not durably retain its interrupted outcome")
		}
		retained, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
		if err != nil {
			t.Fatal(err)
		}
		session, err := store.Decode[domain.Session](retained)
		if err != nil || session.Dispatch != domain.DispatchPaused || session.PendingInputs != 0 || session.Execution == nil {
			t.Fatal("native cancellation discarded durable input/session state")
		}
		if scenario == nativeWorkerRevocation || journal.Problem != nil {
			if session.ActiveExecutionID != f.input.ExecutionID || session.Recovery != domain.NeedsRecovery || session.Execution.CleanupVerified {
				t.Fatal("server confused canceled transport with acknowledged native cleanup")
			}
		} else {
			var completion domain.ExecutionCompletion
			if domain.Decode(journal.Output, &completion) != nil || completion.Validate() != nil || session.ActiveExecutionID != "" || session.Recovery != domain.NoRecovery || !session.Execution.CleanupVerified || session.Outcome != domain.ExecutionStopped {
				t.Fatal("native interruption did not publish terminal cleanup separately from pause")
			}
			assertNativeWorkerCheckpoint(t, f, manager.Root, completion)
		}
		if scenario == nativeWorkerStop || scenario == nativeWorkerArchive || interactionScenario {
			if journal.Problem != nil || session.Outcome != domain.ExecutionStopped || !session.Execution.CleanupVerified {
				t.Fatal("explicit control did not finish its bounded native interruption")
			}
			if (scenario == nativeWorkerArchive) != (session.Archive == domain.Archived) {
				t.Fatal("Stop/Archive lost their independent visibility state")
			}
		}
		if interactionScenario {
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
			if err != nil || len(rows) != 1 {
				t.Fatal("Stop lost retained question")
			}
			interaction, err := store.Decode[domain.ExecutionInteraction](rows[0])
			if err != nil || interaction.Closure == domain.InteractionOpen || session.Execution.Waiting != (domain.NativeWaiting{}) {
				t.Fatal("Stop left an unanswered question active or lost its original content")
			}
			if approvalScenario {
				if interaction.Approval == nil || interaction.Approval.Codex.Command == nil || interaction.Response != nil {
					t.Fatal("Stop discarded approval or invented a response")
				}
			} else if interaction.Questions.Questions[0].Text != "Native Worker question" {
				t.Fatal("Stop discarded original question")
			}
		}
		if session.Execution.CleanupVerified {
			t.Log("targeted control interrupts installed Codex, publishes terminal facts, joins owned cleanup and reports completion while preserving pause/Archive; account readiness remains seeded")
		} else {
			t.Log("revocation cancels installed Codex and loopback inference, joins owned cleanup and retains a failed journal/server recovery without replay; account readiness remains seeded")
		}
		return
	}
	var completed domain.Job
	for {
		changed := f.service.Store.Changed()
		r, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			t.Fatal(err)
		}
		if job.State == domain.JobSucceeded {
			completed = job
			break
		}
		if job.State == domain.JobUncertain || job.State == domain.JobFailed || job.State == domain.JobCanceled {
			outbox, _ := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job), "publication.json"), 2<<20)
			var retained struct {
				Pending *struct {
					Event domain.ExecutionEvent `json:"event"`
				} `json:"pending"`
			}
			if json.Unmarshal(outbox, &retained) == nil && retained.Pending != nil && retained.Pending.Event.Tool != nil && retained.Pending.Event.Tool.Snapshot != nil {
				update := retained.Pending.Event.Tool
				record, readErr := f.service.Store.Get(ctx, domain.MessageKind, update.ID)
				if readErr == nil {
					prior, _ := store.Decode[domain.ExecutionMessage](record)
					if prior.Tool != nil && prior.Tool.Started.Command != nil && update.Snapshot.Command != nil {
						a, b := prior.Tool.Started.Command, update.Snapshot.Command
						t.Logf("retained tool mismatch: command_equal=%t cwd_equal=%t source_before=%s source_after=%s", a.Command == b.Command, a.Cwd == b.Cwd, a.Source, b.Source)
					}
				}
			}
			t.Fatalf("native Worker failed: %s %v", job.State, job.Problem)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("Worker did not report native execution completion")
		}
	}
	expectedCalls, expectedMessages := int64(1), 2
	if toolScenario || approvalResponseScenario {
		expectedCalls, expectedMessages = 2, 3
	}
	if responseScenario {
		expectedCalls = 2
		if err := <-responseAccepted; err != nil {
			t.Fatal(err)
		}
	}
	var completion domain.ExecutionCompletion
	if domain.Decode(completed.Output, &completion) != nil || completion.Validate() != nil || calls.Load() != expectedCalls {
		t.Fatal("Worker completion lacks exact native terminal/cleanup evidence")
	}
	assertNativeWorkerCheckpoint(t, f, manager.Root, completion)
	retained, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](retained)
	expectedActive := domain.ID("")
	if err != nil || session.ActiveExecutionID != expectedActive || session.Execution == nil || !session.Execution.CleanupVerified || session.PendingInputs != 0 || session.Outcome != domain.ExecutionSucceeded {
		t.Fatal("Worker native completion was not atomically published")
	}
	_, terminalInbox := readExecutionInbox(t, f, domain.ExecutionTerminalInbox, f.input.ExecutionID)
	if terminalInbox.ReadState != domain.InboxUnread || terminalInbox.Terminal.Outcome != domain.ExecutionSucceeded || terminalInbox.Terminal.Sequence != session.Execution.LastSequence {
		t.Fatal("native Worker completion did not retain immutable unread inbox evidence")
	}
	if responseScenario && !approvalResponseScenario {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
		if err != nil || len(rows) != 1 {
			t.Fatal("native response lost its original interaction", err)
		}
		interaction, err := store.Decode[domain.ExecutionInteraction](rows[0])
		if err != nil || interaction.Closure == domain.InteractionOpen || interaction.Response == nil || interaction.Response.State != domain.QuestionResponseAccepted || interaction.Response.Acceptance == nil || interaction.Response.Acceptance.Evidence != domain.NativeQuestionOutput || interaction.Response.Claim == nil || interaction.Response.Delivery == nil || interaction.Response.Delivery.State != domain.QuestionTransmitted || len(interaction.Response.Input.Answers["choice"]) != 1 || interaction.Response.Input.Answers["choice"][0] != "Second" {
			t.Fatal("native response lost its claim/transport evidence")
		}
		if session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchReady || session.ActiveExecutionID != "" || session.Execution.UnconfirmedResponses != 0 {
			t.Fatal("exact native answer acceptance failed to reconcile healthy execution")
		}
		_, questionInbox := readExecutionInbox(t, f, domain.InteractionInbox, rows[0].ID)
		if questionInbox.ReadState != domain.InboxRead {
			t.Fatal("native response or closure lost explicit owner inbox read state")
		}
		journal, err := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job), "responses", string(rows[0].ID)+".json"), 64<<10)
		if err != nil || !strings.Contains(string(journal), string(interaction.Response.Claim.ID)) || strings.Contains(string(journal), "Second") || strings.Contains(string(journal), "Native Worker question") {
			t.Fatal("Worker response journal lost ownership or retained answer/question content")
		}
	}
	if approvalResponseScenario {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
		if err != nil || len(rows) != 1 {
			t.Fatal("approval response lost its original request", err)
		}
		interaction, err := store.Decode[domain.ExecutionInteraction](rows[0])
		response := interaction.ApprovalResponse
		if err != nil || interaction.Closure == domain.InteractionOpen || interaction.Approval == nil || response == nil || response.State != domain.ApprovalResponseAccepted || response.Acceptance == nil || response.Acceptance.Evidence != domain.NativeApprovedCommand || response.Claim == nil || response.Delivery == nil || response.Delivery.State != domain.ApprovalTransmitted || response.Input.Decision == nil || response.Input.Decision.Kind != domain.CodexApprovalAccept {
			t.Fatal("approval lost original claim and native transport evidence")
		}
		if session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchReady || session.Execution.UnconfirmedResponses != 0 {
			t.Fatal("single-use native approval execution was not reconciled")
		}
		_, entry := readExecutionInbox(t, f, domain.InteractionInbox, rows[0].ID)
		if entry.ReadState != domain.InboxRead {
			t.Fatal("approval response erased independent inbox state")
		}
		journal, err := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job), "approval-responses", string(rows[0].ID)+".json"), 64<<10)
		if err != nil || !strings.Contains(string(journal), string(response.Claim.ID)) || strings.Contains(string(journal), "native-tool-fixture") || strings.Contains(string(journal), "\"decision\"") {
			t.Fatal("approval journal lost metadata-only ownership", err)
		}
	}
	messages, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(messages) != expectedMessages {
		t.Fatal("native Worker lost its transcript")
	}
	sawTool, sawPlan := false, false
	for _, row := range messages {
		message, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil || message.State != domain.MessageComplete {
			t.Fatal("native Worker retained an incomplete message")
		}
		if message.Role == domain.ProgressMessage {
			sawPlan = true
			progress := message.Progress
			if progress == nil || progress.Kind != domain.PlanProgress || progress.Plan == nil || progress.Plan.Explanation == nil || *progress.Plan.Explanation != "native plan fixture" || len(progress.Plan.Steps) != 1 || progress.Plan.Steps[0].Step != "Retain native plan progress" || progress.Plan.Steps[0].Status != domain.PlanCompleted || message.NativeID != "" {
				t.Fatal("native Worker plan progress lost its exact observation")
			}
		}
		if message.Role == domain.ToolMessage {
			sawTool = true
			tool := message.Tool
			if tool == nil || tool.Started.Kind != domain.CommandTool || tool.Completed == nil || tool.Completed.Status != domain.ToolCompleted || tool.Completed.Command.ExitCode == nil || *tool.Completed.Command.ExitCode != 0 || tool.Completed.Command.AggregatedOutput == nil || !strings.Contains(*tool.Completed.Command.AggregatedOutput, "native-tool-fixture") || (tool.Output != nil && !strings.Contains(*tool.Output, "native-tool-fixture")) {
				t.Fatal("native Worker command transcript lost actual output or completion")
			}
		}
	}
	if (scenario == nativeWorkerCommand || approvalResponseScenario) != sawTool || (scenario == nativeWorkerPlan) != sawPlan {
		t.Fatal("native command was not retained as a dedicated tool")
	}
	// The retained runtime must support later native reconciliation without
	// keeping either the execution bearer or the server's upstream credential.
	if err := filepath.WalkDir(filepath.Join(manager.Root, "runtimes"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "ddv_exec_") || strings.Contains(string(raw), "temporary-upstream-fixture-key") {
			t.Error("private native runtime retained a bearer credential")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	t.Log("actual Worker attach -> outbound claimed job -> real prepared workspace lease -> digest registration -> installed Codex -> server relay -> scripted provider -> durable events -> native cleanup -> completion report; first-selection/account readiness seeded, public dispatch and richer events remain pending")
}

func assertNativeWorkerCheckpoint(t *testing.T, f *publicationFixture, root string, completion domain.ExecutionCompletion) {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(job.Input)
	ref := worker.ExecutionCheckpointRef{JobID: f.job, SessionID: f.input.SessionID, MachineID: f.input.MachineID, HistoryExecutionID: f.input.ExecutionID, AssignmentInputDigest: hex.EncodeToString(digest[:]), ConfigurationDigest: f.input.ConfigurationDigest, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, Completion: completion, InputMode: f.input.Input.Mode, PromptDigest: sha256.Sum256([]byte(f.input.Input.Prompt))}
	checkpoint, err := worker.ReadCodexExecutionCheckpoint(root, ref)
	if err != nil || checkpoint.Native.ThreadID != completion.NativeThreadID || checkpoint.Native.TurnID != completion.NativeTurnID || checkpoint.Native.Effective.Model != f.input.Configuration.NativeModel {
		t.Fatalf("completed native Worker did not retain exact continuation evidence: %v", err)
	}
	var original store.Record
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		original, err = tx.JobAssignment(f.job)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	claimed, err := store.Decode[domain.Job](original)
	var input domain.ExecutionJobInput
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if err != nil || domain.Decode(claimed.Input, &input) != nil || domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil {
		t.Fatal("native inspection lost immutable original assignment", err)
	}
	assignmentDigest := sha256.Sum256(original.Data)
	ref.Completion.Version, ref.Completion.NativeCheckpointDigest = 1, ""
	inspection := worker.CompletedExecutionRef{ServerID: f.service.Identity.ServerID, DeviceID: f.device, InstanceID: claimed.InstanceID, AssignmentRevision: original.Revision, AssignmentDigest: hex.EncodeToString(assignmentDigest[:]), Checkpoint: ref, Preparation: preparation, Manifest: manifest}
	evidence, err := worker.InspectCompletedExecution(context.Background(), &workspace.Manager{Root: root}, inspection)
	if err != nil || evidence.Completion != completion || evidence.JobID != f.job || evidence.ReportID.Validate() != nil {
		t.Fatal("native Worker completion could not be independently inspected", err)
	}
}
