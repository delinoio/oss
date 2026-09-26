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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/credentials"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/opencode"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type openCodeWorkerScenario string

const (
	openCodeWorkerBuild              openCodeWorkerScenario = "build"
	openCodeWorkerPlan               openCodeWorkerScenario = "plan"
	openCodeWorkerStop               openCodeWorkerScenario = "stop"
	openCodeWorkerArchive            openCodeWorkerScenario = "archive"
	openCodeWorkerQuestion           openCodeWorkerScenario = "question"
	openCodeWorkerPermission         openCodeWorkerScenario = "permission"
	openCodeWorkerQuestionStop       openCodeWorkerScenario = "question-stop"
	openCodeWorkerPermissionStop     openCodeWorkerScenario = "permission-stop"
	openCodeWorkerReportLoss         openCodeWorkerScenario = "report-loss"
	openCodeWorkerRevocation         openCodeWorkerScenario = "worker-revocation"
	openCodeWorkerCheckpointConflict openCodeWorkerScenario = "checkpoint-conflict"
)

// These fixtures execute worker.Run and its real outbound job/control stream.
// Account readiness and initial assignment are seeded; no hosted inference or
// user account is used, and first-dispatch acceptance needs its own evidence.
func TestManualNativeOpenCodeWorkerExecutesOriginalAssignment(t *testing.T) {
	for _, scenario := range []openCodeWorkerScenario{openCodeWorkerBuild, openCodeWorkerPlan, openCodeWorkerStop, openCodeWorkerArchive, openCodeWorkerQuestion, openCodeWorkerPermission, openCodeWorkerQuestionStop, openCodeWorkerPermissionStop, openCodeWorkerReportLoss, openCodeWorkerRevocation, openCodeWorkerCheckpointConflict} {
		t.Run(string(scenario), func(t *testing.T) { nativeOpenCodeWorker(t, scenario) })
	}
}

func nativeOpenCodeWorker(t *testing.T, scenario openCodeWorkerScenario) {
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed OpenCode and generated private loopback fixture only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be explicitly absolute")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	question := scenario == openCodeWorkerQuestion || scenario == openCodeWorkerQuestionStop
	permission := scenario == openCodeWorkerPermission || scenario == openCodeWorkerPermissionStop
	interaction := question || permission
	revocation := scenario == openCodeWorkerRevocation
	stopping := scenario == openCodeWorkerStop || scenario == openCodeWorkerArchive || scenario == openCodeWorkerQuestionStop || scenario == openCodeWorkerPermissionStop
	fixtureKey := "Z" + string(domain.NewID())
	var calls atomic.Int64
	var permissionPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		call := calls.Add(1)
		raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
		var request struct {
			Model    string `json:"model"`
			Stream   bool   `json:"stream"`
			Messages []struct {
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
			} `json:"messages"`
		}
		if err != nil || json.Unmarshal(raw, &request) != nil || request.Model != "fixture-model" || !request.Stream || !strings.Contains(string(raw), "Fixture prompt") || r.URL.Path != "/chat/completions" || r.Header.Get("Authorization") != "Bearer "+fixtureKey {
			t.Error("original Worker provider/input authority changed")
			http.Error(w, "unsupported fixture request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		chunk := func(delta any, finish any) {
			value := map[string]any{"id": "chatcmpl-worker", "object": "chat.completion.chunk", "created": 1, "model": "fixture-model", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}}
			if finish != nil {
				value["usage"] = map[string]int{"prompt_tokens": 20, "completion_tokens": 4, "total_tokens": 24}
			}
			raw, _ := json.Marshal(value)
			_, _ = fmt.Fprintf(w, "data: %s\n\n", raw)
		}
		if interaction && call == 1 {
			name := "question"
			var arguments any = map[string]any{"questions": []any{map[string]any{"header": "Choose", "question": "Original Worker choice", "multiple": false, "custom": true, "options": []any{map[string]string{"label": "First", "description": "First choice"}, map[string]string{"label": "Second", "description": "Second choice"}}}}}
			if permission {
				name, arguments = "read", map[string]any{"filePath": permissionPath}
			}
			args, _ := json.Marshal(arguments)
			chunk(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "call_worker_original", "type": "function", "function": map[string]any{"name": name, "arguments": string(args)}}}}, nil)
			chunk(map[string]any{}, "tool_calls")
			_, _ = io.WriteString(w, "data: [DONE]\n\n")
			return
		}
		if stopping || revocation {
			if interaction || call != 1 {
				t.Error("Stop replayed original provider execution")
			}
			chunk(map[string]any{"role": "assistant", "content": "Original Worker text before Stop."}, nil)
			w.(http.Flusher).Flush()
			select {
			case <-r.Context().Done():
			case <-ctx.Done():
				t.Error("Stop did not cancel original inference")
			}
			return
		}
		if interaction {
			result := false
			for _, message := range request.Messages {
				if message.Role == "tool" && (question && strings.Contains(string(message.Content), "Second") || permission && strings.Contains(string(message.Content), "private-worker-file")) {
					result = true
				}
			}
			if call != 2 || !result {
				t.Error("original native tool/answer did not reach the same provider")
			}
		} else if call != 1 {
			t.Error("Worker replayed its original input")
		}
		chunk(map[string]any{"role": "assistant", "content": "Original Worker complete."}, nil)
		chunk(map[string]any{}, "stop")
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	manager := &workspace.Manager{Root: filepath.Join(t.TempDir(), "worker")}
	f := publicationFixtureFromAuthority(t, newProfileAuthorityFixture(t, upstream.URL, domain.OpenCode, domain.OpenAIChat, func(input *domain.ExecutionJobInput) {
		if scenario == openCodeWorkerPlan {
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
		permissionPath = filepath.Join(manifest.PrimaryPath, ".env.private-worker")
		if permission {
			if err := os.WriteFile(permissionPath, []byte("private-worker-file\n"), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}, true))
	secrets := f.service.accountSecrets.(*accountTestSecrets)
	secrets.mu.Lock()
	secrets.values[credentials.Ref{Owner: f.input.AccountID, ID: f.input.ConnectionID, Purpose: credentials.AccountAPI}] = []byte(fixtureKey)
	secrets.mu.Unlock()
	endpoint := f.http.URL
	lostReport := make(chan struct{}, 1)
	if scenario == openCodeWorkerReportLoss {
		original := f.http.Config.Handler
		fault := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == delidevv1connect.WorkerServiceReportWorkProcedure {
				// Commit the real report, then lose only its HTTP acknowledgement.
				retained := httptest.NewRecorder()
				original.ServeHTTP(retained, r)
				if retained.Code != http.StatusOK {
					t.Error("original completion was not accepted before acknowledgement loss")
				}
				http.Error(w, "scripted lost original report response", http.StatusServiceUnavailable)
				select {
				case lostReport <- struct{}{}:
				default:
				}
				return
			}
			original.ServeHTTP(w, r)
		}))
		defer fault.Close()
		endpoint = fault.URL
	}
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: endpoint, ServerID: f.service.Identity.ServerID, DeviceID: f.device, MachineID: f.input.MachineID, PairingID: domain.NewID(), Token: f.workerToken}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(manager.Root, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	checkpointPath := filepath.Join(manager.Root, "jobs", string(f.job), "opencode-checkpoint.json")
	const conflictingCheckpoint = `{"original":"preserved fixture evidence"}`
	if scenario == openCodeWorkerCheckpointConflict {
		if err := security.PrivateDir(filepath.Dir(checkpointPath)); err != nil {
			t.Fatal(err)
		}
		if err := security.WriteAtomic(checkpointPath, []byte(conflictingCheckpoint)); err != nil {
			t.Fatal(err)
		}
	}
	running, stopWorker := context.WithCancel(ctx)
	done := make(chan struct{})
	var workerErr error
	go func() {
		defer close(done)
		workerErr = worker.Run(running, worker.Config{Root: manager.Root, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil))})
	}()
	defer func() { stopWorker(); <-done }()
	controlled := false
	for {
		changed := f.service.Store.Changed()
		record, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
		if err != nil {
			t.Fatal(err)
		}
		job, err := store.Decode[domain.Job](record)
		if err != nil {
			t.Fatal(err)
		}
		if job.State.Terminal() || job.State == domain.JobUncertain {
			if scenario == openCodeWorkerCheckpointConflict {
				if job.State != domain.JobUncertain || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || len(job.Output) != 0 || calls.Load() != 1 {
					t.Fatal("conflicting checkpoint was overwritten or granted completion", job.State, job.Problem)
				}
				original, err := security.ReadPrivate(checkpointPath, 9<<20)
				if err != nil || string(original) != conflictingCheckpoint {
					t.Fatal("checkpoint conflict replaced original retained bytes")
				}
				sr, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err := store.Decode[domain.Session](sr)
				if err != nil || session.Recovery != domain.NeedsRecovery || session.Execution == nil || session.Execution.CleanupVerified || session.Outcome != domain.ExecutionSucceeded {
					t.Fatal("checkpoint failure erased native outcome or fabricated cleanup acceptance")
				}
				if err := process.ReconcileOwnerContext(ctx, filepath.Join(manager.Root, "processes"), f.job); err != nil {
					t.Fatal("checkpoint conflict abandoned original process cleanup", err)
				}
				return
			}
			if !stopping && (job.State != domain.JobSucceeded || job.Problem != nil) || stopping && (job.State != domain.JobCanceled || job.Problem == nil || job.Problem.Code != domain.Canceled) {
				t.Fatalf("original Worker failed: %s %v", job.State, job.Problem)
			}
			var completion domain.ExecutionCompletion
			wantVersion := uint32(2)
			if interaction && (!permission || stopping) {
				wantVersion = 1
			}
			if domain.Decode(job.Output, &completion) != nil || completion.ValidateForHarness(domain.OpenCode) != nil || completion.Version != wantVersion || !completion.CleanupVerified {
				t.Fatal("Worker completion lost original cleanup or invented continuation")
			}
			expected := domain.ExecutionSucceeded
			if stopping {
				expected = domain.ExecutionStopped
			}
			if completion.Outcome != expected {
				t.Fatalf("original native outcome: %s", completion.Outcome)
			}
			sr, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](sr)
			if err != nil || session.Execution == nil || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || session.Outcome != expected || session.PendingInputs != 0 || session.Recovery != domain.NoRecovery {
				t.Fatal("completion did not settle exact original execution")
			}
			if stopping && (session.Dispatch != domain.DispatchPaused || (scenario == openCodeWorkerArchive) != (session.Archive == domain.Archived)) {
				t.Fatal("Stop/Archive state did not survive original cleanup")
			}
			expectedCalls := int64(1)
			if interaction && !stopping {
				expectedCalls = 2
			}
			if calls.Load() != expectedCalls {
				t.Fatal("original provider calls were repeated or lost")
			}
			if err := process.ReconcileOwnerContext(ctx, filepath.Join(manager.Root, "processes"), f.job); err != nil {
				t.Fatal(err)
			}
			checkpoint, err := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job), "opencode-checkpoint.json"), 9<<20)
			var retainedCheckpoint struct {
				Reference struct {
					Completion domain.ExecutionCompletion `json:"completion"`
				} `json:"reference"`
				NativeReference opencode.CheckpointReference `json:"native_reference"`
				Native          json.RawMessage              `json:"native"`
			}
			originalCompletion := completion
			originalCompletion.Version, originalCompletion.NativeCheckpointDigest = 1, ""
			if err != nil || json.Unmarshal(checkpoint, &retainedCheckpoint) != nil || retainedCheckpoint.Reference.Completion != originalCompletion || retainedCheckpoint.NativeReference.OwnerID != f.job || retainedCheckpoint.NativeReference.RequiresResume != stopping || strings.Contains(string(checkpoint), f.input.Input.Prompt) || strings.Contains(string(checkpoint), fixtureKey) {
				t.Fatal("Worker checkpoint lost original completion or disclosed private content")
			}
			if err := opencode.InspectCheckpoint(ctx, filepath.Join(manager.Root, "runtimes", string(f.input.ExecutionID)), retainedCheckpoint.Native, retainedCheckpoint.NativeReference); err != nil {
				t.Fatal("Worker envelope changed the closed original runtime inventory", err)
			}
			if scenario == openCodeWorkerReportLoss {
				select {
				case <-lostReport:
				case <-ctx.Done():
					t.Fatal("original report did not reach acknowledgement-loss boundary")
				}
				stopWorker()
				<-done
				raw, err := security.ReadPrivate(filepath.Join(manager.Root, "jobs", string(f.job)+".json"), 2<<20)
				var retained struct {
					State    string          `json:"state"`
					ReportID domain.ID       `json:"report_id"`
					Output   json.RawMessage `json:"output"`
					Problem  *domain.Error   `json:"problem"`
				}
				var original domain.ExecutionCompletion
				if err != nil || json.Unmarshal(raw, &retained) != nil || retained.State != "finished" || retained.ReportID.Validate() != nil || retained.Problem != nil || domain.Decode(retained.Output, &original) != nil || original != completion || calls.Load() != 1 {
					t.Fatal("lost acknowledgement replaced original durable report or repeated inference")
				}
			}
			if interaction {
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
				if err != nil || len(rows) != 1 {
					t.Fatal("original request missing or duplicated")
				}
				retained, err := store.Decode[domain.ExecutionInteraction](rows[0])
				if err != nil || retained.Closure == domain.InteractionOpen || retained.OpenCode == nil {
					t.Fatal("original request closure missing")
				}
				if stopping && (retained.Response != nil || retained.ApprovalResponse != nil || retained.OpenCodeStop == nil) {
					t.Fatal("Stop invented a reply or omitted its proof")
				}
				_, entry := readExecutionInbox(t, f, domain.InteractionInbox, rows[0].ID)
				if entry.ReadState != domain.InboxUnread {
					t.Fatal("reply/Stop changed original inbox state")
				}
			}
			break
		}
		if !controlled && (stopping || interaction || revocation) {
			ready := false
			if interaction {
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: f.input.SessionID, Limit: 2})
				if err != nil {
					t.Fatal(err)
				}
				if len(rows) == 1 {
					ready = true
					if !stopping {
						client := delidevv1connect.NewInteractionServiceClient(f.http.Client(), f.http.URL)
						mutation := &pb.Mutation{RequestId: string(domain.NewID()), Id: string(rows[0].ID), ExpectedRevision: rows[0].Revision}
						if question {
							body, _ := json.Marshal(domain.QuestionResponseInput{OpenCode: &domain.OpenCodeQuestionResponse{Answers: [][]string{{"Second"}}}})
							_, err = client.RespondQuestion(ctx, ownerRequest(f.service.Identity, &pb.RespondQuestionRequest{Mutation: mutation, ResponseJson: body}))
						} else {
							body, _ := json.Marshal(domain.ApprovalResponseInput{OpenCode: &domain.OpenCodePermissionResponse{Decision: domain.OpenCodePermissionOnce}})
							_, err = client.RespondApproval(ctx, ownerRequest(f.service.Identity, &pb.RespondApprovalRequest{Mutation: mutation, ResponseJson: body}))
						}
						if err != nil {
							t.Fatal(err)
						}
					}
				}
			} else {
				rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 20})
				if err != nil {
					t.Fatal(err)
				}
				for _, row := range rows {
					message, _ := store.Decode[domain.ExecutionMessage](row)
					if strings.Contains(message.Text, "Original Worker text before Stop.") {
						ready = true
					}
				}
			}
			if ready {
				if revocation {
					_, err := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL).RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.device), ExpectedRevision: 1}}))
					if err != nil {
						t.Fatal(err)
					}
					select {
					case <-done:
					case <-ctx.Done():
						t.Fatal("revoked Worker did not join original native lifetime")
					}
					if workerErr == nil || (domain.SafeError(workerErr).Code != domain.Unauthenticated && domain.SafeError(workerErr).Code != domain.PermissionDenied) {
						t.Fatal("Worker lost original revocation classification", workerErr)
					}
					if err := process.ReconcileOwnerContext(ctx, filepath.Join(manager.Root, "processes"), f.job); err != nil {
						t.Fatal(err)
					}
					sr, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					session, err := store.Decode[domain.Session](sr)
					if err != nil || session.Recovery != domain.NeedsRecovery || session.Execution == nil || session.Execution.CleanupVerified || session.ActiveExecutionID != f.input.ExecutionID || calls.Load() != 1 {
						t.Fatal("revocation fabricated acknowledged completion or repeated input")
					}
					if _, err := os.Lstat(checkpointPath); !os.IsNotExist(err) {
						t.Fatal("revoked execution manufactured a closed checkpoint")
					}
					return
				}
				if stopping {
					sr, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
					if err != nil {
						t.Fatal(err)
					}
					action := pb.SessionAction_SESSION_ACTION_STOP
					if scenario == openCodeWorkerArchive {
						action = pb.SessionAction_SESSION_ACTION_ARCHIVE
					}
					_, err = delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL).ControlSession(ctx, ownerRequest(f.service.Identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: action}))
					if err != nil {
						t.Fatal(err)
					}
				}
				controlled = true
			}
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("original Worker did not settle before its fixture deadline")
		}
	}
}
