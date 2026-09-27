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
	"os/exec"
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

func nativeOpenCodeRecoveryExecutable(t *testing.T) string {
	t.Helper()
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
	return binary
}

func TestManualNativeOpenCodeExternalAllowanceRecovery(t *testing.T) {
	binary := nativeOpenCodeRecoveryExecutable(t)
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, tool := range []string{"read", "bash", "glob", "grep", "write", "edit", "apply_patch", "read-cascade"} {
			for _, suffix := range []string{"", "-resumed"} {
				scenario := "always-external-" + tool + suffix
				t.Run(string(mode)+"/"+tool+suffix, func(t *testing.T) { nativeOpenCodeRecovery(t, binary, mode, scenario) })
			}
		}
	}
}

func TestManualNativeOpenCodeCompletedExecutionRecovery(t *testing.T) {
	binary := nativeOpenCodeRecoveryExecutable(t)
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		for _, scenario := range []string{"first", "resumed", "switched", "failed", "missing-checkpoint", "read", "bash", "read-once", "read-always", "read-always-resumed", "read-cascade", "read-cascade-resumed", "question", "question-resumed", "glob", "grep", "todowrite", "todowrite-resumed", "write", "write-resumed", "edit", "edit-resumed", "apply_patch", "apply_patch-resumed", "question-dismissed", "question-dismissed-resumed", "read-reject", "read-reject-resumed", "read-correction", "read-correction-resumed", "read-correction-cascade", "read-correction-cascade-resumed", "read-loaded", "read-loaded-resumed", "reject-external-read", "reject-external-read-resumed", "reject-external-bash", "reject-external-bash-resumed", "reject-external-glob", "reject-external-glob-resumed", "reject-external-grep", "reject-external-grep-resumed", "reject-external-write", "reject-external-write-resumed", "reject-external-edit", "reject-external-edit-resumed", "reject-external-apply_patch", "reject-external-apply_patch-resumed", "read-missing", "read-missing-resumed", "read-missing-once", "read-missing-once-resumed", "read-missing-always", "read-missing-always-resumed"} {
			t.Run(string(mode)+"/"+scenario, func(t *testing.T) { nativeOpenCodeRecovery(t, binary, mode, scenario) })
		}
	}
}

func nativeOpenCodeRecovery(t *testing.T, binary string, mode domain.SessionMode, scenario string) {
	workspaceType, scenario := openCodeProjectFixtureProfile(scenario)
	projectProfile := openCodeCommittedProject
	switch scenario {
	case "unborn-resumed":
		projectProfile, scenario = openCodeUnbornProject, "resumed"
	case "first-commit-resumed":
		projectProfile, scenario = openCodeFirstCommitProject, "resumed"
	}
	missingRead := strings.HasPrefix(scenario, "read-missing")
	loadedInstructions := strings.HasPrefix(scenario, "read-loaded")
	fileTool := strings.TrimSuffix(scenario, "-resumed")
	externalAllowance := strings.HasPrefix(fileTool, "always-external-")
	if externalAllowance {
		fileTool = strings.TrimPrefix(fileTool, "always-external-")
	}
	externalRejection := strings.HasPrefix(fileTool, "reject-external-")
	if externalRejection {
		fileTool = strings.TrimPrefix(fileTool, "reject-external-")
	}
	rejection := externalRejection || strings.HasPrefix(scenario, "read-reject") || strings.HasPrefix(scenario, "read-correction")
	correction := strings.HasPrefix(scenario, "read-correction")
	rejectionCascade := rejection && strings.HasSuffix(fileTool, "-cascade")
	if (fileTool == "bash" || openCodeContinuationFileTool(fileTool)) && mode == domain.PlanMode {
		t.Skip("native Plan shell/file policy needs separate interaction restoration")
	}
	search := fileTool == "glob" || fileTool == "grep"
	if search {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip("native search requires existing ripgrep; no automatic download")
		}
	}
	todo := strings.HasPrefix(scenario, "todowrite")
	question := strings.HasPrefix(scenario, "question")
	dismissed := strings.HasPrefix(scenario, "question-dismissed")
	stoppedFirst := dismissed || rejection && (!correction || rejectionCascade)
	tool := ""
	toolCalls := int64(0)
	cascade := externalAllowance && fileTool == "read-cascade" || strings.HasPrefix(scenario, "read-cascade") || rejectionCascade
	remembered := externalAllowance || strings.HasPrefix(scenario, "read-missing-always") || strings.HasPrefix(scenario, "read-always") || cascade && !rejection
	permissionCount := 1
	if cascade {
		permissionCount = 2
	}
	permission := fileTool == "read-missing-once" || scenario == "read-once" || remembered || rejection
	if scenario == "read" || scenario == "bash" || missingRead || loadedInstructions || permission || question || search || todo || openCodeContinuationFileTool(fileTool) {
		tool, toolCalls = scenario, 1
		if stoppedFirst {
			toolCalls = 0
		}
		if openCodeContinuationFileTool(fileTool) {
			tool = fileTool
		}
		if todo {
			tool = "todowrite"
		}
		if question {
			tool = "question"
		}
		if permission || loadedInstructions || missingRead {
			tool = "read"
		}
		if externalRejection || externalAllowance {
			tool = fileTool
			if externalAllowance && cascade {
				tool = "read"
			}
		}
	}
	fixtureModel := openCodeContinuationModel(tool)
	var toolPath atomic.Value
	var originalToolResult string
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	var calls atomic.Int64
	lostTurn := int64(1)
	if scenario == "resumed" || scenario == "switched" || scenario == "read-always-resumed" || scenario == "read-cascade-resumed" || scenario == "question-resumed" || scenario == "question-dismissed-resumed" || scenario == "todowrite-resumed" || (openCodeContinuationFileTool(fileTool) || rejection || loadedInstructions || missingRead || externalAllowance) && strings.HasSuffix(scenario, "-resumed") {
		lostTurn = 2
	}
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("keyless upstream received credentials")
		}
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			_, _ = io.WriteString(w, strings.ReplaceAll(`{"data":[{"id":"fixture-model","object":"model"}]}`, "fixture-model", fixtureModel))
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
			if rejection {
				result = verifyOpenCodeRejectedTools(t, raw, tool, toolPath.Load().(string), permissionCount, correction)
			} else if cascade {
				results := verifyOpenCodeRepeatedRead(t, raw, toolPath.Load().(string), 2, 2)
				encoded, _ := json.Marshal(results)
				result = string(encoded)
			} else if missingRead {
				result = verifyOpenCodeContinuationResult(t, raw, tool, toolPath.Load().(string), "File not found: "+toolPath.Load().(string))
			} else {
				result = verifyOpenCodeContinuationTool(t, raw, tool, toolPath.Load().(string), dismissed)
			}
			if loadedInstructions {
				verifyOpenCodeLoadedInstructions(t, result, toolPath.Load().(string), true)
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
		_, _ = io.WriteString(w, strings.ReplaceAll(`data: {"id":"chatcmpl-recovery","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original recovery completion."},"finish_reason":null}]}`+"\n\n", "fixture-model", fixtureModel))
		_, _ = io.WriteString(w, strings.ReplaceAll(`data: {"id":"chatcmpl-recovery","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n", "fixture-model", fixtureModel))
	}))
	defer upstream.Close()
	f := newFirstDispatchFixtureWorkspaceProfile(t, domain.OpenCode, mode, binary, upstream.URL, fixtureModel, workspaceType, projectProfile)
	if missingRead {
		toolPath.Store(prepareOpenCodeMissingRead(t, f, permission))
	} else if externalRejection || externalAllowance {
		toolPath.Store(prepareOpenCodeExternalTool(t, tool))
		if externalAllowance && tool == "bash" {
			if err := os.Remove(toolPath.Load().(string)); err != nil {
				t.Fatal(err)
			}
		}
	} else if loadedInstructions {
		toolPath.Store(prepareOpenCodeLoadedRead(t, f, false))
	} else if tool != "" {
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
				if rejection {
					decision = domain.OpenCodePermissionReject
				}
				meta := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(rows[0].ID), ExpectedRevision: rows[0].Revision}
				if question {
					body, _ := json.Marshal(openCodeContinuationQuestionResponse(dismissed))
					_, err = client.RespondQuestion(ctx, ownerRequest(f.identity, &pb.RespondQuestionRequest{Mutation: meta, ResponseJson: body}))
				} else {
					response := &domain.OpenCodePermissionResponse{Decision: decision}
					if correction {
						feedback := openCodeContinuationCorrection
						response.Feedback = &feedback
					}
					body, _ := json.Marshal(domain.ApprovalResponseInput{OpenCode: response})
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
	var adoptedDirectory string
	if lostTurn == 2 {
		firstState := domain.JobSucceeded
		if stoppedFirst {
			firstState = domain.JobCanceled
		}
		if job := waitJob(domain.ID(assignment.Id)); job.State != firstState {
			t.Fatal("first execution failed", job.Problem)
		}
		if projectProfile == openCodeFirstCommitProject {
			var original domain.Job
			var execution domain.ExecutionJobInput
			var manifest workspace.Manifest
			if domain.Decode(assignment.DocumentJson, &original) != nil || domain.Decode(original.Input, &execution) != nil || domain.Decode(execution.Manifest, &manifest) != nil {
				t.Fatal("missing original Local manifest")
			}
			adoptedDirectory = manifest.PrimaryPath
			commitOpenCodeProjectFixture(t, ctx, adoptedDirectory)
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
			if (externalRejection || externalAllowance) && (retained.OpenCode == nil || retained.OpenCode.Permission == nil || retained.OpenCode.Permission.Name != "external_directory") {
				t.Fatal("missing original external-directory rejection")
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
	if missingRead {
		verifyOpenCodeMissingReadFile(t, toolPath.Load().(string), true)
	}
	if loadedInstructions {
		updateOpenCodeContinuationInstructions(t, toolPath.Load().(string), true)
	}
	if externalAllowance && !openCodeContinuationFileTool(tool) {
		path := toolPath.Load().(string)
		if raw, err := os.ReadFile(path); err != nil || string(raw) != "original-inline-tool-sentinel\n" {
			t.Fatal("original external tool changed its source before recovery", err)
		}
		if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if externalRejection {
		path := toolPath.Load().(string)
		verifyOpenCodeExternalRejectionFiles(t, path, tool, false)
		if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
			t.Fatal(err)
		}
	} else if openCodeContinuationFileTool(tool) {
		path := toolPath.Load().(string)
		expected := "original-inline-tool-sentinel edited\n"
		if tool == "write" {
			expected = "original-inline-tool-sentinel\n"
		}
		if value, err := os.ReadFile(path); err != nil || string(value) != expected {
			t.Fatal("original file mutation missing before recovery", err)
		}
		if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
			t.Fatal(err)
		}
		if tool == "apply_patch" {
			verifyOpenCodeContinuationPatchFiles(t, path, 0)
		}
	}
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
	if stoppedFirst && lostTurn == 1 {
		expectedOutcome, expectedState = domain.ExecutionStopped, domain.JobCanceled
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
	if adoptedDirectory != "" {
		for path, want := range map[string]string{"tracked.txt": "Later independent Local changes.\n", "untracked-after-first-commit.txt": "Retain untracked Local content.\n"} {
			raw, err := os.ReadFile(filepath.Join(adoptedDirectory, path))
			if err != nil || string(raw) != want {
				t.Fatal("recovered adoption changed independent Local files", err)
			}
		}
	}
	if loadedInstructions {
		updateOpenCodeContinuationInstructions(t, toolPath.Load().(string), false)
	}
	if missingRead {
		verifyOpenCodeMissingReadFile(t, toolPath.Load().(string), false)
	}
	if externalAllowance && !openCodeContinuationFileTool(tool) {
		if raw, err := os.ReadFile(toolPath.Load().(string)); err != nil || string(raw) != "changed-source-after-original-tool\n" {
			t.Fatal("recovery replayed the original external tool", err)
		}
	}
	if externalRejection {
		verifyOpenCodeExternalRejectionFiles(t, toolPath.Load().(string), tool, true)
	} else if rejection {
		if value, err := os.ReadFile(toolPath.Load().(string)); err != nil || string(value) != "original-inline-tool-sentinel\n" {
			t.Fatal("recovery changed rejected Read source", err)
		}
	}
	if openCodeContinuationFileTool(tool) && !externalRejection {
		path := toolPath.Load().(string)
		if value, err := os.ReadFile(path); err != nil || string(value) != "changed-source-after-original-tool\n" {
			t.Fatal("recovery or replacement replayed original file mutation", err)
		}
		if tool == "apply_patch" {
			verifyOpenCodeContinuationPatchFiles(t, path, 1)
		}
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
