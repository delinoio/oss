package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestManualNativeOpenCodePublicFirstDispatch(t *testing.T) {
	nativeOpenCodePublicDispatch(t, 1, false, "")
}

func TestManualNativeOpenCodePublicContinuation(t *testing.T) {
	nativeOpenCodePublicDispatch(t, 3, false, "")
}

func TestManualNativeOpenCodePublicModeTransitions(t *testing.T) {
	nativeOpenCodePublicDispatch(t, 3, false, "", true)
}

func TestManualNativeOpenCodePublicModeTransitionAfterFailure(t *testing.T) {
	nativeOpenCodePublicDispatch(t, 3, true, "", true)
}

func TestManualNativeOpenCodePublicResumeAfterFailure(t *testing.T) {
	nativeOpenCodePublicDispatch(t, 3, true, "")
}

func TestManualNativeOpenCodePublicContinuationRefusesChangedEvidence(t *testing.T) {
	for _, fault := range []string{"checkpoint", "database", "claims", "report", "outbox"} {
		t.Run(fault, func(t *testing.T) { nativeOpenCodePublicDispatch(t, 2, false, fault) })
	}
}

func TestManualNativeOpenCodePublicOncePermissionContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "read-once", true)
}

func TestManualNativeOpenCodePublicAlwaysPermissionContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "read-always", true)
}

func TestManualNativeOpenCodePublicPolicyPermissionContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "read-cascade", true)
}

func TestManualNativeOpenCodePublicRejectionContinuation(t *testing.T) {
	for _, profile := range []string{"read-reject", "read-reject-empty", "read-correction", "read-reject-cascade", "read-correction-cascade"} {
		t.Run(profile, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", profile, true) })
	}
}

func TestManualNativeOpenCodePublicQuestionContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "question", true)
}

func TestManualNativeOpenCodePublicQuestionDismissalContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "question-dismissed", true)
}

func TestManualNativeOpenCodePublicTodoClearContinuation(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, false, "", "todo-clear", true)
}

func TestManualNativeOpenCodePublicSearchTodoContinuation(t *testing.T) {
	for _, tool := range []string{"glob", "grep", "todowrite"} {
		t.Run(tool, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", tool, true) })
	}
}

func TestManualNativeOpenCodePublicFileToolContinuation(t *testing.T) {
	for _, tool := range []string{"write", "edit", "apply_patch"} {
		t.Run(tool, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", tool, true) })
	}
}

func TestManualNativeOpenCodePublicExternalRejectionContinuation(t *testing.T) {
	for _, tool := range []string{"read", "bash", "glob", "grep", "write", "edit", "apply_patch"} {
		t.Run(tool, func(t *testing.T) {
			nativeOpenCodePublicDispatchProfile(t, 3, false, "", "reject-external-"+tool, true)
		})
	}
}

func TestManualNativeOpenCodePublicExternalCorrectionContinuation(t *testing.T) {
	for _, profile := range []string{"reject-external-read-mixed", "correct-external-read-mixed", "correct-external-write"} {
		t.Run(profile, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", profile, true) })
	}
}

func TestManualNativeOpenCodePublicExternalAllowanceContinuation(t *testing.T) {
	for _, tool := range []string{"read", "bash", "glob", "grep", "write", "edit", "apply_patch", "read-cascade"} {
		t.Run(tool, func(t *testing.T) {
			nativeOpenCodePublicDispatchProfile(t, 3, false, "", "always-external-"+tool, true)
		})
	}
}

func TestManualNativeOpenCodePublicReadErrorContinuation(t *testing.T) {
	for _, profile := range []string{"read-missing", "read-missing-once", "read-missing-always"} {
		t.Run(profile, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", profile, true) })
	}
}

func TestManualNativeOpenCodePublicReadErrorAfterFailure(t *testing.T) {
	nativeOpenCodePublicDispatchProfile(t, 3, true, "", "read-missing", true)
}

func TestManualNativeOpenCodePublicLoadedInstructionsContinuation(t *testing.T) {
	for _, profile := range []string{"read-loaded", "read-loaded-always"} {
		t.Run(profile, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", profile, true) })
	}
}

func TestManualNativeOpenCodePublicInlineToolContinuation(t *testing.T) {
	for _, tool := range []string{"read", "bash"} {
		t.Run(tool, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", tool, true) })
	}
}

func nativeOpenCodePublicDispatch(t *testing.T, turns int, failedFirst bool, fault string, switchModes ...bool) {
	nativeOpenCodePublicDispatchProfile(t, turns, failedFirst, fault, "", switchModes...)
}

func nativeOpenCodePublicDispatchProfile(t *testing.T, turns int, failedFirst bool, fault, tool string, switchModes ...bool) {
	workspaceType, tool := openCodeProjectFixtureProfile(tool)
	projectProfile := openCodeCommittedProject
	if strings.HasPrefix(tool, "multiple-") {
		projectProfile, tool = openCodeMultipleProject, strings.TrimPrefix(tool, "multiple-")
	}
	if workspaceType == domain.Local && (tool == "unborn" || tool == "first-commit") {
		projectProfile = openCodeUnbornProject
		if tool == "first-commit" {
			projectProfile = openCodeFirstCommitProject
		}
		tool = ""
	}
	externalAllowance := strings.HasPrefix(tool, "always-external-")
	if externalAllowance {
		tool = strings.TrimPrefix(tool, "always-external-")
	}
	externalCorrection := strings.HasPrefix(tool, "correct-external-")
	externalRejection := strings.HasPrefix(tool, "reject-external-") || externalCorrection
	if externalRejection {
		tool = strings.TrimPrefix(strings.TrimPrefix(tool, "reject-external-"), "correct-external-")
	}
	externalMixed := externalRejection && strings.HasSuffix(tool, "-mixed")
	if externalMixed {
		tool = strings.TrimSuffix(tool, "-mixed")
	}
	missingRead := strings.HasPrefix(tool, "read-missing")
	loadedInstructions := strings.HasPrefix(tool, "read-loaded")
	rejection := externalRejection || strings.HasPrefix(tool, "read-reject") || strings.HasPrefix(tool, "read-correction")
	correction := externalCorrection || strings.HasPrefix(tool, "read-correction")
	rejectionCascade := externalMixed || rejection && strings.HasSuffix(tool, "-cascade")
	emptyFeedback := tool == "read-reject-empty"
	dismissed := tool == "question-dismissed"
	stoppedFirst := dismissed || rejection && (!correction || rejectionCascade)
	if dismissed {
		tool = "question"
	}
	repeatTodo := tool == "todo-clear"
	if repeatTodo {
		tool = "todowrite"
	}
	search := tool == "glob" || tool == "grep"
	if search {
		if _, err := exec.LookPath("rg"); err != nil {
			t.Skip("native search requires existing ripgrep; no automatic download")
		}
	}
	question := tool == "question"
	cascade := tool == "read-cascade" || rejectionCascade
	permission := externalAllowance || tool == "read-missing-once" || tool == "read-missing-always" || tool == "read-once" || tool == "read-always" || tool == "read-loaded-always" || cascade || rejection
	always := externalAllowance || tool == "read-missing-always" || tool == "read-always" || tool == "read-loaded-always" || cascade && !rejection
	permissionCount := 1
	if cascade {
		permissionCount = 2
	}
	if (permission || loadedInstructions || missingRead) && !externalRejection && !externalAllowance {
		tool = "read"
	}
	if externalAllowance && cascade {
		tool = "read"
	}
	binary := os.Getenv("DELIDEV_NATIVE_OPENCODE_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit installed OpenCode with public APIs and generated loopback provider only")
	}
	if !filepath.IsAbs(binary) {
		t.Fatal("native executable must be absolute")
	}
	binary, err := filepath.EvalSymlinks(binary)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []domain.SessionMode{domain.ExecuteMode, domain.PlanMode} {
		t.Run(string(mode), func(t *testing.T) {
			if (tool == "bash" || openCodeContinuationFileTool(tool)) && mode == domain.PlanMode {
				t.Skip("native Plan shell/file policy is a separate interaction profile")
			}
			fixtureModel := openCodeContinuationModel(tool)
			var toolPath atomic.Value
			var originalToolResult string
			retainedTools := map[string]string{}
			expectedCalls := turns
			if tool != "" && !stoppedFirst {
				expectedCalls++
			}
			if always && !missingRead || loadedInstructions || repeatTodo {
				expectedCalls = turns * 2
			}
			modeAt := func(turn int) domain.SessionMode {
				if len(switchModes) == 1 && switchModes[0] && turn%2 == 1 {
					if mode == domain.ExecuteMode {
						return domain.PlanMode
					}
					return domain.ExecuteMode
				}
				return mode
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			var calls atomic.Int64
			var referenceManifest atomic.Value
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("keyless upstream received Worker or owner credentials")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, strings.ReplaceAll(`{"data":[{"id":"fixture-model","object":"model"}]}`, "fixture-model", fixtureModel))
					return
				}
				raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				if projectProfile == openCodeMultipleProject {
					verifyOpenCodeProjectReferences(t, raw, referenceManifest.Load().(workspace.Manifest))
				}
				var request struct {
					Model  string `json:"model"`
					Stream bool   `json:"stream"`
				}
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || err != nil || json.Unmarshal(raw, &request) != nil || request.Model != fixtureModel || !request.Stream || !strings.Contains(string(raw), "first retained input") || calls.Add(1) > int64(expectedCalls) {
					t.Error("public dispatch changed original native request")
					http.Error(w, "unsupported", 400)
					return
				}
				providerTurn := int(calls.Load())
				if repeatTodo {
					completed := providerTurn / 2
					providerTurn = (providerTurn + 1) / 2
					results := verifyOpenCodeRepeatedTodo(t, raw, completed)
					for id, result := range results {
						if prior, exists := retainedTools[id]; exists && prior != result {
							t.Error("replacement altered original Todo result")
						}
						retainedTools[id] = result
					}
					if calls.Load()%2 == 1 {
						serveOpenCodeContinuationArguments(t, w, tool, openCodeTodoArguments(providerTurn-1), fmt.Sprintf("%s_%d", continuationToolCall, providerTurn-1))
						return
					}
				} else if always && !missingRead || loadedInstructions {
					completeTools := providerTurn / 2
					if cascade && providerTurn > 1 {
						completeTools++
					}
					providerTurn = (providerTurn + 1) / 2
					results := verifyOpenCodeRepeatedTools(t, raw, toolPath.Load().(string), completeTools, permissionCount, tool)
					for id, result := range results {
						if loadedInstructions {
							verifyOpenCodeLoadedInstructions(t, result, toolPath.Load().(string), id == continuationToolCall+"_0")
						}
						if old, found := retainedTools[id]; found && old != result {
							t.Error("replacement altered original remembered Read result")
						}
						retainedTools[id] = result
					}
					if calls.Load()%2 == 1 {
						ids := []string{fmt.Sprintf("%s_%d", continuationToolCall, completeTools)}
						if cascade && providerTurn == 1 {
							ids = append(ids, fmt.Sprintf("%s_1", continuationToolCall))
						}
						if externalAllowance {
							nextTool := tool
							if completeTools >= permissionCount {
								nextTool = "read"
							}
							requests := make([]openCodeFixtureToolCall, 0, len(ids))
							for _, id := range ids {
								requests = append(requests, openCodeFixtureToolCall{nextTool, openCodeContinuationToolArguments(nextTool, toolPath.Load().(string)), id})
							}
							serveOpenCodeContinuationCallsModel(t, w, requests, fixtureModel)
						} else {
							serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string), ids...)
						}
						return
					}
				} else if rejection {
					if providerTurn == 1 {
						ids := []string{continuationToolCall}
						if cascade {
							ids = []string{continuationToolCall + "_0", continuationToolCall + "_1"}
						}
						if externalMixed {
							path := toolPath.Load().(string)
							serveOpenCodeContinuationCalls(t, w, []openCodeFixtureToolCall{{"read", openCodeContinuationToolArguments("read", path), ids[0]}, {"write", openCodeContinuationToolArguments("write", path), ids[1]}})
						} else {
							serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string), ids...)
						}
						return
					}
					if !stoppedFirst {
						providerTurn--
					}
					var secondTool []string
					if externalMixed {
						secondTool = []string{"write"}
					}
					result := verifyOpenCodeRejectedTools(t, raw, tool, toolPath.Load().(string), permissionCount, correction, secondTool...)
					if originalToolResult == "" {
						originalToolResult = result
					} else if result != originalToolResult {
						t.Error("replacement changed original rejection/correction")
					}
				} else if tool != "" {
					if providerTurn == 1 {
						serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string))
						return
					}
					if !dismissed {
						providerTurn--
					}
					var result string
					if missingRead {
						result = verifyOpenCodeContinuationResult(t, raw, tool, toolPath.Load().(string), "File not found: "+toolPath.Load().(string))
					} else {
						result = verifyOpenCodeContinuationTool(t, raw, tool, toolPath.Load().(string), dismissed)
					}
					if originalToolResult == "" {
						originalToolResult = result
					} else if result != originalToolResult {
						t.Error("replacement changed original tool result")
					}
				}
				if failedFirst && providerTurn == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"error":{"message":"Private fixture failure","type":"invalid_api_key"}}`)
					return
				}
				for prior := 1; prior < providerTurn; prior++ {
					if !strings.Contains(string(raw), fmt.Sprintf("continuation input %d", prior)) {
						t.Error("native provider omitted queued original input", prior)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, strings.ReplaceAll(`data: {"id":"chatcmpl-public","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Public original completion."},"finish_reason":null}]}`+"\n\n", "fixture-model", fixtureModel))
				_, _ = io.WriteString(w, strings.ReplaceAll(`data: {"id":"chatcmpl-public","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n", "fixture-model", fixtureModel))
			}))
			defer upstream.Close()
			// Configuration, pairing, keyless account validation, session creation,
			// preparation report and explicit first Resume use their public APIs. Only
			// protocol discovery is a pinned reported fixture; actual initialization is
			// independently revalidated by the original installed native process.
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
				toolPath.Store(prepareOpenCodeLoadedRead(t, f, permission))
			} else if tool != "" {
				toolPath.Store(prepareOpenCodeContinuationTool(t, f, tool, permission))
			}
			if projectProfile == openCodeMultipleProject {
				manifest := openCodeProjectManifest(t, f)
				referenceManifest.Store(manifest)
				if len(manifest.Repositories) != 3 || manifest.Repositories[1].Path != manifest.PrimaryPath {
					t.Fatal("fixture lost designated non-first primary")
				}
				if tool != "" {
					old := toolPath.Load().(string)
					path := filepath.Join(manifest.Repositories[0].Path, filepath.Base(old))
					if tool != "write" {
						if err := os.Rename(old, path); err != nil {
							t.Fatal(err)
						}
					}
					toolPath.Store(path)
				}
			}
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
			done, ready := make(chan struct{}), make(chan domain.ID, 1)
			go func() {
				defer close(done)
				_ = worker.Run(running, worker.Config{Root: f.workerRoot, Logger: slog.New(slog.NewJSONHandler(os.Stderr, nil)), Ready: func(id domain.ID) { ready <- id }})
			}()
			defer func() { stop(); <-done }()
			select {
			case <-ready:
			case <-done:
				t.Fatal("original Worker failed before readiness")
			case <-ctx.Done():
				t.Fatal("original Worker did not attach")
			}
			for next := 1; next < turns; next++ {
				body, _ := json.Marshal(domain.SessionInput{Prompt: fmt.Sprintf("continuation input %d", next), Mode: modeAt(next)})
				if _, err := sessionClient(f.accountFixture).EnqueueInput(ctx, ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: body})); err != nil {
					t.Fatal(err)
				}
			}
			sr := f.refresh(t)
			response, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
			if err != nil {
				t.Fatal(err)
			}
			assignment := response.Msg.Change.ExecutionJob
			if assignment == nil {
				t.Fatal("public Resume did not create first immutable execution")
			}
			var firstThread domain.NativeIdentity
			var previous domain.ExecutionCompletion
			responded := false
			originalPermissions := map[domain.ID][]byte{}
			for turn := 0; turn < turns; turn++ {
				var job domain.Job
				var input domain.ExecutionJobInput
				if domain.Decode(assignment.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Input.Mode != modeAt(turn) || input.Configuration.Harness != domain.OpenCode || (input.Continuation != nil) != (turn > 0) {
					t.Fatal("public assignment changed exact native input")
				}
				if turn > 0 && (input.Continuation.Completion != previous || input.Continuation.Previous.NativeThreadID != string(firstThread)) {
					t.Fatal("continuation replaced original accepted predecessor")
				}
				for {
					changed := f.service.Store.Changed()
					if (permission || question) && !responded {
						rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 3})
						if err != nil || len(rows) > permissionCount {
							t.Fatal("unexpected original permission inventory", err)
						}
						if len(rows) == permissionCount {
							client := delidevv1connect.NewInteractionServiceClient(http.DefaultClient, f.endpoint.URL)
							decision := domain.OpenCodePermissionOnce
							if always {
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
								} else if emptyFeedback {
									feedback := ""
									response.Feedback = &feedback
								}
								body, _ := json.Marshal(domain.ApprovalResponseInput{OpenCode: response})
								_, err = client.RespondApproval(ctx, ownerRequest(f.identity, &pb.RespondApprovalRequest{Mutation: meta, ResponseJson: body}))
							}
							if err != nil {
								t.Fatal(err)
							}
							responded = true
						}
					}
					record, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(assignment.Id))
					if err != nil {
						t.Fatal(err)
					}
					completed, err := store.Decode[domain.Job](record)
					if err != nil {
						t.Fatal(err)
					}
					if completed.State.Terminal() || completed.State == domain.JobUncertain {
						if fault != "" && turn > 0 {
							if completed.State != domain.JobUncertain || completed.Problem == nil || completed.Problem.Code != domain.RecoveryRequired || len(completed.Output) != 0 || calls.Load() != 1 {
								t.Fatal("changed predecessor acquired replacement input authority", completed.State, completed.Problem)
							}
							if _, err := os.Lstat(filepath.Join(f.workerRoot, "runtimes", string(input.ExecutionID))); !os.IsNotExist(err) {
								t.Fatal("unverified predecessor launched a replacement runtime")
							}
							return
						}
						var proof domain.ExecutionCompletion
						expectedState, expectedOutcome, expectedDispatch := domain.JobSucceeded, domain.ExecutionSucceeded, domain.DispatchReady
						expectedVersion := uint32(2)
						wantCalls := turn + 1 + expectedCalls - turns
						if always && !missingRead || loadedInstructions || repeatTodo {
							wantCalls = (turn + 1) * 2
						}
						if failedFirst && turn == 0 {
							expectedState, expectedOutcome, expectedDispatch = domain.JobFailed, domain.ExecutionFailed, domain.DispatchPaused
						}
						if stoppedFirst && turn == 0 {
							expectedState, expectedOutcome, expectedDispatch = domain.JobCanceled, domain.ExecutionStopped, domain.DispatchPaused
						}
						if completed.State != expectedState || domain.Decode(completed.Output, &proof) != nil || proof.ValidateForHarness(domain.OpenCode) != nil || proof.Version != expectedVersion || proof.ExecutionID != input.ExecutionID || proof.InputID != input.InputID || !proof.CleanupVerified || calls.Load() != int64(wantCalls) {
							t.Fatalf("public native execution did not retain original completion: %s %v", completed.State, completed.Problem)
						}
						session, err := store.Decode[domain.Session](f.refresh(t))
						if err != nil || session.Execution == nil || !session.Execution.CleanupVerified || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || session.PendingInputs != uint32(turns-turn-1) || session.Dispatch != expectedDispatch || session.Outcome != expectedOutcome {
							t.Fatal("version-2 completion lost continuation intent or cleanup")
						}
						if turn == 0 {
							firstThread = proof.NativeThreadID
						} else if proof.NativeThreadID != firstThread || proof.NativeTurnID == previous.NativeTurnID || proof.ExecutionID == previous.ExecutionID || proof.NativeCheckpointDigest == previous.NativeCheckpointDigest {
							t.Fatal("continuation recreated native history or reused execution authority")
						}
						if missingRead {
							verifyOpenCodeMissingReadFile(t, toolPath.Load().(string), turn == 0)
						} else if externalRejection {
							verifyOpenCodeExternalRejectionFiles(t, toolPath.Load().(string), tool, turn > 0)
							if turn == 0 {
								if err := os.WriteFile(toolPath.Load().(string), []byte("changed-source-after-original-tool\n"), 0600); err != nil {
									t.Fatal(err)
								}
							}
						} else if tool == "read" || tool == "bash" || openCodeContinuationFileTool(tool) {
							path := toolPath.Load().(string)
							content, err := os.ReadFile(path)
							expected := "original-inline-tool-sentinel\n"
							if tool == "edit" || tool == "apply_patch" {
								expected = "original-inline-tool-sentinel edited\n"
							}
							if (tool == "read" || openCodeContinuationFileTool(tool) || externalAllowance && tool == "bash") && turn > 0 {
								expected = "changed-source-after-original-tool\n"
							}
							if err != nil || string(content) != expected {
								t.Fatal("original tool was replayed or its workspace output changed")
							}
							if turn == 0 && (tool == "read" || openCodeContinuationFileTool(tool) || externalAllowance && tool == "bash") {
								if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
									t.Fatal(err)
								}
							}
						}
						if loadedInstructions {
							updateOpenCodeContinuationInstructions(t, toolPath.Load().(string), turn == 0)
						}
						if tool == "apply_patch" && !externalRejection {
							verifyOpenCodeContinuationPatchFiles(t, toolPath.Load().(string), turn)
						}
						if search && !externalRejection && turn == 0 {
							path := toolPath.Load().(string)
							if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
								t.Fatal(err)
							}
							if err := os.WriteFile(filepath.Join(filepath.Dir(path), "changed-source-after-original-tool.txt"), []byte("changed"), 0600); err != nil {
								t.Fatal(err)
							}
						}
						if permission || question {
							rows, err := f.service.Store.List(ctx, store.Filter{Kind: domain.InteractionKind, SessionID: domain.ID(f.change.Session.Id), Limit: 3})
							if err != nil || len(rows) != permissionCount || !responded {
								t.Fatal("original permission missing or duplicated")
							}
							direct, policy := 0, 0
							for _, row := range rows {
								retained, err := store.Decode[domain.ExecutionInteraction](row)
								if err != nil || retained.Closure == domain.InteractionOpen {
									t.Fatal("original permission closure lost")
								}
								if (externalRejection || externalAllowance) && (retained.OpenCode == nil || retained.OpenCode.Permission == nil || retained.OpenCode.Permission.Name != "external_directory") {
									t.Fatal("fixture did not retain the original external-directory request")
								}
								if question {
									if retained.Response == nil || retained.Response.State != domain.QuestionResponseAccepted || retained.ApprovalResponse != nil {
										t.Fatal("original question acceptance lost")
									}
									direct++
								} else if retained.ApprovalResponse != nil {
									if retained.ApprovalResponse.State != domain.ApprovalResponseAccepted {
										t.Fatal("original direct acceptance lost")
									}
									direct++
								} else {
									if !cascade || retained.OpenCodeClosure == nil {
										t.Fatal("original native policy closure lost")
									}
									policy++
								}
								if turn == 0 {
									originalPermissions[row.ID] = bytes.Clone(row.Data)
								} else if !bytes.Equal(originalPermissions[row.ID], row.Data) {
									t.Fatal("replacement rewrote original approval or policy closure")
								}
							}
							if direct != 1 || policy != permissionCount-1 {
								t.Fatal("policy closure acquired a direct response")
							}
						}
						previous = proof
						if err := process.ReconcileOwnerContext(ctx, filepath.Join(f.workerRoot, "processes"), domain.ID(assignment.Id)); err != nil {
							t.Fatal(err)
						}
						break
					}
					select {
					case <-changed:
					case <-ctx.Done():
						t.Fatal("public native execution did not finish")
					}
				}
				if projectProfile == openCodeFirstCommitProject {
					var manifest workspace.Manifest
					if domain.Decode(input.Manifest, &manifest) != nil {
						t.Fatal("invalid original Local manifest")
					}
					if turn == 0 {
						commitOpenCodeProjectFixture(t, ctx, manifest.PrimaryPath)
					} else {
						for path, want := range map[string]string{"tracked.txt": "Later independent Local changes.\n", "untracked-after-first-commit.txt": "Retain untracked Local content.\n"} {
							raw, err := os.ReadFile(filepath.Join(manifest.PrimaryPath, path))
							if err != nil || string(raw) != want {
								t.Fatal("native adoption changed later Local files", err)
							}
						}
					}
				}
				if turn+1 < turns {
					if fault != "" && turn == 0 {
						alterOpenCodeContinuationEvidence(t, ctx, f.workerRoot, domain.ID(assignment.Id), input.ExecutionID, fault)
					}
					if (failedFirst || stoppedFirst) && turn == 0 {
						if f.service.dispatchExecution(ctx, f.refresh(t)) == nil {
							t.Fatal("failed input gained automatic continuation")
						}
						current := f.refresh(t)
						if _, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(current.ID), ExpectedRevision: current.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME})); err != nil {
							t.Fatal("explicit Resume after failure rejected", err)
						}
					} else if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
						t.Fatal("automatic FIFO continuation failed", err)
					}
					var next store.Record
					if err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
						_, session, err := sessionRecord(tx, sr.ID)
						if err != nil {
							return err
						}
						next, err = tx.SessionExecutionJob(sr.ID, session.ActiveExecutionID)
						return err
					}); err != nil {
						t.Fatal(err)
					}
					assignment = &pb.Resource{Id: string(next.ID), DocumentJson: next.Data}
				}
			}
		})
	}
}

// Wait for the original report journal to finish its acknowledgment transition
// before injecting a fault; never race a legitimate writer into repairing it.
func alterOpenCodeContinuationEvidence(t *testing.T, ctx context.Context, root string, job, execution domain.ID, fault string) {
	t.Helper()
	report := filepath.Join(root, "jobs", string(job)+".json")
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		raw, err := security.ReadPrivate(report, 2<<20)
		var value struct {
			State string `json:"state"`
		}
		if err == nil && json.Unmarshal(raw, &value) == nil && value.State == "reported" {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("original report journal did not finish")
		}
	}
	path := filepath.Join(root, "jobs", string(job), "opencode-checkpoint.json")
	switch fault {
	case "database":
		path = filepath.Join(root, "runtimes", string(execution), "data", "opencode", "opencode.db")
	case "snapshot":
		path = filepath.Join(root, "runtimes", string(execution), "snapshot-checkpoint", "index")
	case "reference-config":
		path = filepath.Join(root, "runtimes", string(execution), "opencode", "opencode.json")
	case "claims":
		path = filepath.Join(root, "jobs", string(job), "opencode-claims.json")
	case "report":
		path = report
	case "outbox":
		path = filepath.Join(root, "jobs", string(job), "publication.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fault == "report" || fault == "outbox" {
		var value map[string]json.RawMessage
		if json.Unmarshal(raw, &value) != nil {
			t.Fatal("invalid fixture journal")
		}
		if fault == "report" {
			value["state"] = json.RawMessage(`"started"`)
		} else {
			value["last_sequence"] = json.RawMessage(`0`)
		}
		raw, _ = json.Marshal(value)
	} else {
		raw = append(raw, '\n')
	}
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
}
