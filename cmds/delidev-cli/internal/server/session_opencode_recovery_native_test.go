package server

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestManualNativeOpenCodeCompletedExecutionRecovery(t *testing.T) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed OpenCode and generated loopback provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be absolute")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []string{"first", "resumed", "switched", "failed", "missing-checkpoint", "read", "bash", "read-once", "read-always", "read-always-resumed", "read-cascade", "read-cascade-resumed", "question", "question-resumed"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) { nativeOpenCodeRecovery(t, binary, mode, scenario) })
		}
	}
}

func nativeOpenCodeRecovery(t *testing.T, binary string, mode domain.SessionMode, scenario string) {
	if scenario == "bash" && mode == domain.PlanMode {
		t.Skip("native Plan shell policy needs separate interaction restoration")
	}
	question := strings.HasPrefix(scenario, "question")
	tool := ""
	toolCalls := int64(0)
	cascade := strings.HasPrefix(scenario, "read-cascade")
	remembered := strings.HasPrefix(scenario, "read-always") || cascade
	permissionCount := 1
	if cascade {
		permissionCount = 2
	}
	permission := scenario == "read-once" || remembered
	if scenario == "read" || scenario == "bash" || permission || question {
		tool, toolCalls = scenario, 1
		if question {
			tool = "question"
		}
		if permission {
			tool = "read"
		}
	}
	var toolPath atomic.Value
	var originalToolResult string
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var calls atomic.Int64
	lostTurn := int64(1)
	if scenario == "resumed" || scenario == "switched" || scenario == "read-always-resumed" || scenario == "read-cascade-resumed" || scenario == "question-resumed" {
		lostTurn = 2
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("keyless upstream received credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || err != nil || !strings.Contains(string(raw), "first retained input") || calls.Add(1) > lostTurn+1+toolCalls {
			t.Error("recovery repeated or changed native inference")
			http.Error(w, "unsupported", http.StatusBadRequest)
			return
		}
		if tool != "" {
			if calls.Load() == 1 {
				if cascade {
					serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string), continuationToolCall+"_0", continuationToolCall+"_1")
				} else {
					serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string))
				}
				return
			}
			result := ""
			if cascade {
				results := verifyOpenCodeRepeatedRead(t, raw, toolPath.Load().(string), 2, 2)
				encoded, _ := json.Marshal(results)
				result = string(encoded)
			} else {
				result = verifyOpenCodeContinuationTool(t, raw, tool, toolPath.Load().(string))
			}
			if originalToolResult == "" {
				originalToolResult = result
			} else if result != originalToolResult {
				t.Error("recovery altered original tool result")
			}
		}
		if calls.Load() > 1+toolCalls && !strings.Contains(string(raw), "following retained input") {
			t.Error("native continuation lost input history")
		}
		if scenario == "failed" && calls.Load() == 1 {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"error":{"message":"Private fixture failure","type":"invalid_api_key"}}`)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-recovery","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original recovery completion."},"finish_reason":null}]}`+"\n\n")
		_, _ = io.WriteString(w, `data: {"id":"chatcmpl-recovery","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
	}))
	defer upstream.Close()
	f := newFirstDispatchFixtureProfile(t, domain.OpenCode, mode, binary, upstream.URL)
	if tool != "" {
		toolPath.Store(prepareOpenCodeContinuationTool(t, f, tool, permission))
	}
	f.workerStream.Close()
	expire := func() {
		_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.expire-recovery-worker", nil, func(tx *store.Tx) (any, error) {
			instance, _, err := tx.WorkerInstance(f.selection.MachineID)
			if err != nil {
				return nil, err
			}
			return nil, tx.SetWorkerInstance(f.selection.MachineID, instance, time.Now().Add(-2*time.Minute))
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	expire()
	lostReport := make(chan struct{}, 1)
	var allowReports atomic.Bool
	originalHandler := f.service.Handler(nil, true)
	fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == delidevv1connect.WorkerServiceReportWorkProcedure && calls.Load() >= lostTurn+toolCalls && !allowReports.Load() {
			select {
			case lostReport <- struct{}{}:
			default:
			}
			http.Error(w, "scripted lost report before acceptance", http.StatusServiceUnavailable)
			return
		}
		originalHandler.ServeHTTP(w, r)
	}))
	defer fault.Close()
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: fault.URL, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	start := func() func() {
		running, stop := context.WithCancel(ctx)
		done, ready := make(chan struct{}), make(chan domain.ID, 1)
		go func() {
			defer close(done)
			_ = worker.Run(running, worker.Config{Root: f.workerRoot, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), Ready: func(id domain.ID) {
				select {
				case ready <- id:
				default:
				}
			}})
		}()
		join := func() { stop(); <-done }
		t.Cleanup(join)
		select {
		case <-ready:
		case <-done:
			t.Fatal("Worker failed before readiness")
		case <-ctx.Done():
			t.Fatal("Worker did not attach")
		}
		return join
	}
	stop := start()
	client := sessionClient(f.accountFixture)
	inputMode := mode
	enqueue := func() {
		if scenario == "switched" {
			if inputMode == domain.ExecuteMode {
				inputMode = domain.PlanMode
			} else {
				inputMode = domain.ExecuteMode
			}
		}
		raw, _ := json.Marshal(domain.SessionInput{Prompt: "following retained input", Mode: inputMode})
		if _, err := client.EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: raw})); err != nil {
			t.Fatal(err)
		}
	}
	resume := func() *pb.Resource {
		sr := f.refresh(t)
		response, err := client.ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
		if err != nil || response.Msg.Change.ExecutionJob == nil {
			t.Fatal("public Resume failed", err)
		}
		return response.Msg.Change.ExecutionJob
	}
	waitJob := func(id domain.ID) domain.Job {
		for {
			changed := f.service.Store.Changed()
			record, err := f.service.Store.Get(ctx, domain.JobKind, id)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](record)
			if err != nil {
				t.Fatal(err)
			}
			if job.State.Terminal() || job.State == domain.JobUncertain {
				return job
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatal("Worker job did not finish")
			}
		}
	}
	assignment := resume()

	originalPermissions := map[domain.ID][]byte{}
	if permission || question {
		for {
			changed := f.service.Store.Changed()
			rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 3})
			if err != nil || len(rows) > permissionCount {
				t.Fatal("unexpected original permission inventory", err)
			}
			if len(rows) == permissionCount {
				client := delidevv1connect.NewInteractionServiceClient(http.DefaultClient, f.endpoint.URL)
				decision := domain.OpenCodePermissionOnce
				if remembered {
					decision = domain.OpenCodePermissionAlways
				}
				meta := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(rows[0].ID), ExpectedRevision: rows[0].Revision}
				if question {
					body, _ := json.Marshal(domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: openCodeContinuationAnswers()}})
					_, err = client.RespondQuestion(ctx, ownerRequest(f.identity, &pb.RespondQuestionRequest{Mutation: meta, ResponseJson: body}))
				} else {
					body, _ := json.Marshal(domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: decision}})
					_, err = client.RespondApproval(ctx, ownerRequest(f.identity, &pb.RespondApprovalRequest{Mutation: meta, ResponseJson: body}))
				}
				if err != nil {
					t.Fatal(err)
				}
				break
			}
			select {
			case <-changed:
			case <-ctx.Done():
				t.Fatal("original permission did not arrive")
			}
		}
	}
	if lostTurn == 2 {
		if job := waitJob(domain.ID(assignment.Id)); job.State != domain.JobSucceeded {
			t.Fatal("first execution failed", job.Problem)
		}
		enqueue()
		assignment = resume()
	}
	select {
	case <-lostReport:
	case <-ctx.Done():
		t.Fatal("original completion never reached report fault")
	}
	if permission || question {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 3})
		if err != nil || len(rows) != permissionCount {
			t.Fatal("original permission missing")
		}
		direct, policy := 0, 0
		for _, row := range rows {
			retained, err := store.Decode[domain.ExecutionInteraction](row)
			if err != nil || retained.Closure == domain.InteractionOpen {
				t.Fatal("original permission closure missing")
			}
			if question {
				if retained.Response == nil || retained.Response.State != domain.QuestionResponseAccepted || retained.ApprovalResponse != nil {
					t.Fatal("original question acceptance missing")
				}
				direct++
			} else if retained.ApprovalResponse != nil {
				if retained.ApprovalResponse.State != domain.ApprovalResponseAccepted {
					t.Fatal("original permission acceptance missing")
				}
				direct++
			} else {
				if !cascade || retained.OpenCodeClosure == nil {
					t.Fatal("original automatic policy closure missing")
				}
				policy++
			}
			originalPermissions[row.ID] = bytes.Clone(row.Data)
		}
		if direct != 1 || policy != permissionCount-1 {
			t.Fatal("automatic policy acquired a direct response")
		}
	}
	stop()
	expire()
	originalID := domain.ID(assignment.Id)
	operationPath := filepath.Join(f.workerRoot, "jobs", assignment.Id+".json")
	original, err := security.ReadPrivate(operationPath, 2<<20)
	var operation struct {
		ReportID domain.ID       `json:"report_id"`
		Output   json.RawMessage `json:"output"`
	}
	var completion domain.ExecutionCompletion
	if err != nil || json.Unmarshal(original, &operation) != nil || operation.ReportID.Validate() != nil || domain.Decode(operation.Output, &completion) != nil || completion.Version != 2 {
		t.Fatal("original private completion missing")
	}
	checkpointPath := filepath.Join(f.workerRoot, "jobs", assignment.Id, "opencode-checkpoint.json")
	checkpoint, err := security.ReadPrivate(checkpointPath, 9<<20)
	if err != nil {
		t.Fatal(err)
	}
	if scenario == "missing-checkpoint" {
		if err := os.Remove(checkpointPath); err != nil {
			t.Fatal(err)
		}
	}
	allowReports.Store(true)
	stopReplacement := start()
	defer stopReplacement()
	sr := f.refresh(t)
	request := &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, ExpectedExecutionId: string(completion.ExecutionID)}
	response, err := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, request))
	if err != nil || response.Msg.Change.ExecutionRecoveryJob == nil {
		t.Fatal("public recovery failed", err)
	}
	var recoveryJob domain.Job
	var comparison domain.ExecutionRecoveryRequest
	if domain.Decode(response.Msg.Change.ExecutionRecoveryJob.DocumentJson, &recoveryJob) != nil || domain.Decode(recoveryJob.Input, &comparison) != nil || comparison.Validate() != nil || comparison.Harness != domain.OpenCode || comparison.InputMode != inputMode || comparison.OpenCode.ClaimVersion != uint32(lostTurn) {
		t.Fatal("recovery changed native comparison authority")
	}
	job := waitJob(domain.ID(response.Msg.Change.ExecutionRecoveryJob.Id))
	session, err := store.Decode[domain.Session](f.refresh(t))
	expectedOutcome, expectedState := domain.ExecutionSucceeded, domain.JobSucceeded
	if scenario == "failed" {
		expectedOutcome, expectedState = domain.ExecutionFailed, domain.JobFailed
	}
	if err != nil || calls.Load() != lostTurn+toolCalls || session.Outcome != expectedOutcome || session.Dispatch != domain.DispatchPaused || session.NextExecutionIntent != "" {
		t.Fatal("recovery changed outcome, resumed, or repeated input")
	}
	after, err := security.ReadPrivate(operationPath, 2<<20)
	if err != nil || !bytes.Equal(original, after) {
		t.Fatal("recovery rewrote original operation journal")
	}
	if scenario == "missing-checkpoint" {
		if job.State == domain.JobSucceeded || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || session.Recovery != domain.NeedsRecovery || session.Execution.CleanupVerified {
			t.Fatal("missing checkpoint became completion proof")
		}
		if _, err := os.Lstat(checkpointPath); !os.IsNotExist(err) {
			t.Fatal("recovery reconstructed missing evidence")
		}
		return
	}
	var evidence domain.ExecutionRecoveryEvidence
	if job.State != domain.JobSucceeded || domain.Decode(job.Output, &evidence) != nil || evidence.Validate(comparison) != nil || evidence.ReportID != operation.ReportID || evidence.Completion != completion || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || !session.Execution.CleanupVerified {
		t.Fatal("recovery lost original completion or pause", job.Problem)
	}
	assertOpenCodeRecoveryComparison(t, ctx, f.workerRoot, comparison, evidence)
	changed, err := security.ReadPrivate(checkpointPath, 9<<20)
	if err != nil || !bytes.Equal(checkpoint, changed) {
		t.Fatal("recovery modified native checkpoint")
	}
	reconciled := waitJob(originalID)
	var accepted domain.ExecutionCompletion
	if reconciled.State != expectedState || domain.Decode(reconciled.Output, &accepted) != nil || accepted != completion {
		t.Fatal("original job not reconciled atomically")
	}
	replayed, err := client.RecoverSessionExecution(ctx, ownerRequest(f.identity, request))
	if err != nil || !replayed.Msg.Change.Replayed || replayed.Msg.Change.ExecutionRecoveryJob.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
		t.Fatal("accepted recovery receipt created new work", err)
	}
	enqueue()
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err == nil {
		t.Fatal("recovery authorized automatic dispatch")
	}
	assignment = resume()
	next := waitJob(domain.ID(assignment.Id))
	var continued domain.ExecutionCompletion
	if next.State != domain.JobSucceeded || domain.Decode(next.Output, &continued) != nil || continued.NativeThreadID != completion.NativeThreadID || continued.NativeTurnID == completion.NativeTurnID || calls.Load() != lostTurn+1+toolCalls {
		t.Fatal("explicit Resume failed after recovered completion", next.Problem)
	}
	if permission || question {
		rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 3})
		if err != nil || len(rows) != permissionCount {
			t.Fatal("recovery or replacement changed original accepted permission")
		}
		for _, row := range rows {
			if !bytes.Equal(originalPermissions[row.ID], row.Data) {
				t.Fatal("recovery rewrote original approval or policy closure")
			}
		}
	}
	t.Log("lost original native report -> joined old Worker -> replacement inspection -> exact paused recovery -> explicit same-session Resume; no inference or mutation replay during recovery")
}

func assertOpenCodeRecoveryComparison(t *testing.T, ctx context.Context, root string, request domain.ExecutionRecoveryRequest, expected domain.ExecutionRecoveryEvidence) {
	t.Helper()
	var preparation workspace.PrepareRequest
	var manifest workspace.Manifest
	if domain.Decode(request.Preparation, &preparation) != nil || domain.Decode(request.Manifest, &manifest) != nil {
		t.Fatal("invalid original workspace comparison")
	}
	digest, _ := hex.DecodeString(request.PromptDigest)
	ref := worker.CompletedExecutionRef{
		Harness: request.Harness, OpenCode: request.OpenCode, ServerID: request.ServerID, DeviceID: request.DeviceID, InstanceID: request.InstanceID,
		AssignmentRevision: request.AssignmentRevision, AssignmentDigest: request.AssignmentDigest, Preparation: preparation, Manifest: manifest,
		Checkpoint: worker.ExecutionCheckpointRef{JobID: request.JobID, SessionID: request.SessionID, MachineID: request.MachineID,
			HistoryExecutionID: request.HistoryExecutionID, AssignmentInputDigest: request.AssignmentInputDigest, ConfigurationDigest: request.ConfigurationDigest,
			AccountID: request.AccountID, ConnectionID: request.ConnectionID, Completion: request.Completion, InputMode: request.InputMode, AcceptedInputs: request.AcceptedInputs},
	}
	copy(ref.Checkpoint.PromptDigest[:], digest)
	manager := &workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	proof, err := worker.InspectCompletedExecution(ctx, manager, ref)
	if err != nil || proof != expected {
		t.Fatal("original recovery evidence changed on read", err)
	}
	for _, change := range []func(*worker.CompletedExecutionRef){
		func(r *worker.CompletedExecutionRef) { r.Harness = domain.Codex },
		func(r *worker.CompletedExecutionRef) { r.OpenCode = nil },
		func(r *worker.CompletedExecutionRef) { r.OpenCode.CreationRequestID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.OpenCode.BindingRequestID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.OpenCode.InputRequestID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.InstanceID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.DeviceID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.AssignmentRevision++ },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.HistoryExecutionID = domain.NewID() },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.AssignmentInputDigest = strings.Repeat("ab", 32) },
		func(r *worker.CompletedExecutionRef) { r.Checkpoint.Completion.LastSequence++ },
		func(r *worker.CompletedExecutionRef) { r.Manifest.PrimaryPath = filepath.Dir(r.Manifest.PrimaryPath) },
	} {
		next, native := ref, *ref.OpenCode
		next.OpenCode = &native
		change(&next)
		if _, err := worker.InspectCompletedExecution(ctx, manager, next); err == nil {
			t.Fatal("changed original comparison evidence accepted")
		}
	}
}
