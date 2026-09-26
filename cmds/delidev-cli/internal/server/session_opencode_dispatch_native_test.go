package server

import (
	"context"
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
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestManualNativeOpenCodePublicFirstDispatch(t *testing.T) {
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
				if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || err != nil || json.Unmarshal(raw, &request) != nil || request.Model != "fixture-model" || !request.Stream || !strings.Contains(string(raw), "first retained input") || calls.Add(1) != 1 {
					t.Error("public dispatch changed original native request")
					http.Error(w, "unsupported", 400)
					return
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
			sr := f.refresh(t)
			response, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
			if err != nil {
				t.Fatal(err)
			}
			assignment := response.Msg.Change.ExecutionJob
			if assignment == nil {
				t.Fatal("public Resume did not create first immutable execution")
			}
			var job domain.Job
			var input domain.ExecutionJobInput
			if domain.Decode(assignment.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil || input.Input.Mode != mode || input.Configuration.Harness != domain.OpenCode || input.Continuation != nil {
				t.Fatal("public assignment changed exact native input")
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
					var proof domain.ExecutionCompletion
					if completed.State != domain.JobSucceeded || completed.Problem != nil || domain.Decode(completed.Output, &proof) != nil || proof.ValidateForHarness(domain.OpenCode) != nil || proof.Version != 1 || proof.ExecutionID != input.ExecutionID || proof.InputID != input.InputID || !proof.CleanupVerified || calls.Load() != 1 {
						t.Fatalf("public native execution did not retain original completion: %s %v", completed.State, completed.Problem)
					}
					session, err := store.Decode[domain.Session](f.refresh(t))
					if err != nil || session.Execution == nil || !session.Execution.CleanupVerified || session.Recovery != domain.NoRecovery || session.ActiveExecutionID != "" || session.PendingInputs != 0 || session.Dispatch != domain.DispatchPaused || session.Outcome != domain.ExecutionSucceeded {
						t.Fatal("version-1 completion invented continuation or lost cleanup")
					}
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
		})
	}
}
