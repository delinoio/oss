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

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
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

func TestManualNativeOpenCodePublicInlineToolContinuation(t *testing.T) {
	for _, tool := range []string{"read", "bash"} {
		t.Run(tool, func(t *testing.T) { nativeOpenCodePublicDispatchProfile(t, 3, false, "", tool, true) })
	}
}

func nativeOpenCodePublicDispatch(t *testing.T, turns int, failedFirst bool, fault string, switchModes ...bool) {
	nativeOpenCodePublicDispatchProfile(t, turns, failedFirst, fault, "", switchModes...)
}

func nativeOpenCodePublicDispatchProfile(t *testing.T, turns int, failedFirst bool, fault, tool string, switchModes ...bool) {
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
			if tool == "bash" && mode == domain.PlanMode {
				t.Skip("native Plan shell policy is a separate interaction profile")
			}
			var toolPath atomic.Value
			var originalToolResult string
			expectedCalls := turns
			if tool != "" {
				expectedCalls++
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
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("keyless upstream received Worker or owner credentials")
				}
				if r.Method == http.MethodGet && r.URL.Path == "/models" {
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
					return
				}
				raw, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
				var request struct {
					Model  string `json:"model"`
					Stream bool   `json:"stream"`
				}
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || err != nil || json.Unmarshal(raw, &request) != nil || request.Model != "fixture-model" || !request.Stream || !strings.Contains(string(raw), "first retained input") || calls.Add(1) > int64(expectedCalls) {
					t.Error("public dispatch changed original native request")
					http.Error(w, "unsupported", 400)
					return
				}
				if failedFirst && calls.Load() == 1 {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = io.WriteString(w, `{"error":{"message":"Private fixture failure","type":"invalid_api_key"}}`)
					return
				}
				providerTurn := int(calls.Load())
				if tool != "" {
					if providerTurn == 1 {
						serveOpenCodeContinuationTool(t, w, tool, toolPath.Load().(string))
						return
					}
					providerTurn--
					result := verifyOpenCodeContinuationTool(t, raw, tool, toolPath.Load().(string))
					if originalToolResult == "" {
						originalToolResult = result
					} else if result != originalToolResult {
						t.Error("replacement changed original tool result")
					}
				}
				for prior := 1; prior < providerTurn; prior++ {
					if !strings.Contains(string(raw), fmt.Sprintf("continuation input %d", prior)) {
						t.Error("native provider omitted queued original input", prior)
					}
				}
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-public","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Public original completion."},"finish_reason":null}]}`+"\n\n")
				_, _ = io.WriteString(w, `data: {"id":"chatcmpl-public","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":20,"completion_tokens":4,"total_tokens":24}}`+"\n\ndata: [DONE]\n\n")
			}))
			defer upstream.Close()
			// Configuration, pairing, keyless account validation, session creation,
			// preparation report and explicit first Resume use their public APIs. Only
			// protocol discovery is a pinned reported fixture; actual initialization is
			// independently revalidated by the original installed native process.
			f := newFirstDispatchFixtureProfile(t, domain.OpenCode, mode, binary, upstream.URL)
			if tool != "" {
				toolPath.Store(prepareOpenCodeContinuationTool(t, f, tool))
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
						if failedFirst && turn == 0 {
							expectedState, expectedOutcome, expectedDispatch = domain.JobFailed, domain.ExecutionFailed, domain.DispatchPaused
						}
						if completed.State != expectedState || domain.Decode(completed.Output, &proof) != nil || proof.ValidateForHarness(domain.OpenCode) != nil || proof.Version != 2 || proof.ExecutionID != input.ExecutionID || proof.InputID != input.InputID || !proof.CleanupVerified || calls.Load() != int64(turn+1+expectedCalls-turns) {
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
						if tool != "" {
							path := toolPath.Load().(string)
							content, err := os.ReadFile(path)
							expected := "original-inline-tool-sentinel\n"
							if tool == "read" && turn > 0 {
								expected = "changed-source-after-original-tool\n"
							}
							if err != nil || string(content) != expected {
								t.Fatal("original tool was replayed or its workspace output changed")
							}
							if turn == 0 && tool == "read" {
								if err := os.WriteFile(path, []byte("changed-source-after-original-tool\n"), 0600); err != nil {
									t.Fatal(err)
								}
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
				if turn+1 < turns {
					if fault != "" && turn == 0 {
						alterOpenCodeContinuationEvidence(t, ctx, f.workerRoot, domain.ID(assignment.Id), input.ExecutionID, fault)
					}
					if failedFirst && turn == 0 {
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
