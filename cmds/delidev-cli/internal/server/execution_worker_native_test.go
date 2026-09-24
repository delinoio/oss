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
	nativeWorkerRevocation
	nativeWorkerDisconnect
	nativeWorkerStop
	nativeWorkerArchive
)

func TestManualNativeWorkerExecutesAcceptedCodexJob(t *testing.T) {
	testManualNativeWorkerExecution(t, nativeWorkerCompletion)
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
	var calls atomic.Int64
	started, upstreamStopped := make(chan struct{}, 1), make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodPost || r.URL.Path != "/responses" || r.Header.Get("Authorization") != "Bearer temporary-upstream-fixture-key" {
			t.Error("Worker inference escaped server-only API authority")
			http.Error(w, "unsupported", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil || !strings.Contains(string(body), "Fixture prompt") || !strings.Contains(string(body), "fixture-model") {
			t.Error("Worker changed the accepted native input/model")
		}
		if scenario != nativeWorkerCompletion {
			started <- struct{}{}
			<-r.Context().Done()
			upstreamStopped <- struct{}{}
			return
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
		workerErr = worker.Run(running, worker.Config{Root: manager.Root, Logger: f.service.logger})
	}()
	t.Cleanup(func() { stopWorker(); <-done })
	if scenario != nativeWorkerCompletion {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("native Worker did not reach its owned inference request")
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
				break
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
		select {
		case <-upstreamStopped:
		case <-ctx.Done():
			t.Fatal("revocation retained the native inference request")
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
		}
		if scenario == nativeWorkerStop || scenario == nativeWorkerArchive {
			if journal.Problem != nil || session.Outcome != domain.ExecutionStopped || !session.Execution.CleanupVerified {
				t.Fatal("explicit control did not finish its bounded native interruption")
			}
			if (scenario == nativeWorkerArchive) != (session.Archive == domain.Archived) {
				t.Fatal("Stop/Archive lost their independent visibility state")
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
			t.Fatalf("native Worker failed: %s %v", job.State, job.Problem)
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("Worker did not report native execution completion")
		}
	}
	var completion domain.ExecutionCompletion
	if domain.Decode(completed.Output, &completion) != nil || completion.Validate() != nil || calls.Load() != 1 {
		t.Fatal("Worker completion lacks exact native terminal/cleanup evidence")
	}
	retained, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](retained)
	if err != nil || session.ActiveExecutionID != "" || session.Execution == nil || !session.Execution.CleanupVerified || session.PendingInputs != 0 || session.Outcome != domain.ExecutionSucceeded {
		t.Fatal("Worker native completion was not atomically published")
	}
	messages, err := f.service.Store.List(ctx, store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(messages) != 2 {
		t.Fatal("native Worker lost its transcript")
	}
	for _, row := range messages {
		message, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil || message.State != domain.MessageComplete {
			t.Fatal("native Worker retained an incomplete message")
		}
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
