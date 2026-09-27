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
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/process"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type grokPublicScenario uint8

const (
	grokPublicFirstText grokPublicScenario = iota
	grokPublicCleanupFailure
	grokPublicCancellation
	grokPublicStoppedText
	grokPublicStoppedCleanupFailure
	grokPublicPendingTextStop
	grokPublicPendingCleanupFailure
)

func TestManualNativeGrokPublicFirstTextDispatch(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicFirstText)
}

func TestManualNativeGrokPublicCleanupFailure(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicCleanupFailure)
}

func TestManualNativeGrokPublicCancellationContainment(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicCancellation)
}

func TestManualNativeGrokPublicStoppedText(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicStoppedText)
}

func TestManualNativeGrokPublicStoppedCleanupFailure(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicStoppedCleanupFailure)
}

func TestManualNativeGrokPublicPendingTextStop(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicPendingTextStop)
}
func TestManualNativeGrokPublicPendingStopCleanupFailure(t *testing.T) {
	nativeGrokPublicFirstText(t, grokPublicPendingCleanupFailure)
}

func nativeGrokPublicFirstText(t *testing.T, scenario grokPublicScenario) {
	t.Helper()
	cleanupFailure, cancelled := scenario == grokPublicCleanupFailure || scenario == grokPublicStoppedCleanupFailure || scenario == grokPublicPendingCleanupFailure, scenario == grokPublicCancellation
	pending := scenario == grokPublicPendingTextStop || scenario == grokPublicPendingCleanupFailure
	stopped := scenario == grokPublicStoppedText || scenario == grokPublicStoppedCleanupFailure || pending
	binary := os.Getenv("DELIDEV_NATIVE_GROK_EXECUTABLE")
	if binary == "" {
		t.Skip("explicit pinned native Grok and scripted loopback provider required")
	}
	var err error
	binary, err = filepath.EvalSymlinks(binary)
	if err != nil || !filepath.IsAbs(binary) {
		t.Fatal("native executable is not canonical", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	var calls atomic.Uint32
	started, upstreamStopped := make(chan struct{}, 1), make(chan struct{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			t.Error("keyless provider received a Worker credential")
		}
		if r.Method == http.MethodGet && r.URL.Path == "/models" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"data":[{"id":"fixture-model","object":"model"}]}`)
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20+1))
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if r.Method != http.MethodPost || r.URL.Path != "/chat/completions" || err != nil || len(raw) > 1<<20 || json.Unmarshal(raw, &request) != nil || request.Model != "fixture-model" || !request.Stream || r.Header.Get("HTTP-Referer") != "https://deli.dev" {
			t.Error("original Grok request escaped registered profile")
			w.WriteHeader(400)
			return
		}
		if calls.Add(1) == 1 && !bytes.Contains(raw, []byte("first retained input")) {
			t.Error("original native input was changed")
		}
		if cancelled || stopped {
			if stopped && !pending {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"id\":\"chatcmpl-grok-stop\",\"object\":\"chat.completion.chunk\",\"created\":1,\"model\":\"fixture-model\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"Original public Grok completion.\"},\"finish_reason\":null}]}\n\n")
				w.(http.Flusher).Flush()
			}
			started <- struct{}{}
			<-r.Context().Done()
			upstreamStopped <- struct{}{}
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range []string{
			`{"id":"chatcmpl-grok-original","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{"role":"assistant","content":"Original public Grok completion."},"finish_reason":null}]}`,
			`{"id":"chatcmpl-grok-original","object":"chat.completion.chunk","created":1,"model":"fixture-model","choices":[{"index":0,"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":11,"completion_tokens":5,"total_tokens":16}}`,
		} {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
		}
		_, _ = io.WriteString(w, "data: [DONE]\n\n")
	}))
	defer upstream.Close()
	// Public configuration, keyless validation, preparation and Resume are real
	// RPC flows. Discovery is reported fixture evidence; the runner independently
	// validates this original installed process without real hosted credentials.
	f := newFirstDispatchFixtureProfile(t, domain.GrokBuild, domain.ExecuteMode, binary, upstream.URL)
	f.workerStream.Close()
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.release-grok-setup-worker", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(f.selection.MachineID, domain.ID(f.workerInstance), time.Now().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	var publications, reports atomic.Uint32
	var original atomic.Pointer[domain.ExecutionJobInput]
	var jobID atomic.Value
	assignmentReady := make(chan struct{})
	handler := f.service.Handler(nil, true)
	faultServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		drop := false
		if r.URL.Path == delidevv1connect.WorkerServicePublishExecutionProcedure {
			terminalSequence := uint32(5)
			if stopped {
				terminalSequence = 4
			}
			if pending {
				terminalSequence = 3
			}
			number := publications.Add(1)
			if cancelled && number == 2 {
				// Preserve a committed input acceptance with its original acknowledgment
				// blocked. Startup cancellation must retain uncertainty and never claim Stop.
				retained := httptest.NewRecorder()
				handler.ServeHTTP(retained, r)
				if retained.Code != http.StatusOK {
					t.Error("input acceptance did not commit", retained.Code)
				}
				<-r.Context().Done()
				return
			}
			drop = number == terminalSequence
			if drop && cleanupFailure {
				select {
				case <-assignmentReady:
				case <-r.Context().Done():
					return
				}
				// Simulate loss of the independently retained workspace claim only
				// after native closure. A terminal receipt cannot repair this loss.
				if err := security.WriteAtomic(filepath.Join(f.workerRoot, "execution-claims", string(original.Load().SessionID)+".json"), []byte(`{}`)); err != nil {
					t.Error(err)
				}
			}
		}
		if r.URL.Path == delidevv1connect.WorkerServiceReportWorkProcedure {
			reports.Add(1)
			select {
			case <-assignmentReady:
			case <-r.Context().Done():
				return
			}
			input := original.Load()
			if input == nil {
				t.Error("report preceded immutable assignment observation")
				w.WriteHeader(500)
				return
			}
			var preparation workspace.PrepareRequest
			var manifest workspace.Manifest
			if domain.Decode(input.Preparation, &preparation) != nil || domain.Decode(input.Manifest, &manifest) != nil {
				t.Error("invalid original workspace evidence")
				w.WriteHeader(500)
				return
			}
			manager := &workspace.Manager{Root: f.workerRoot}
			inspection, err := manager.InspectClosedExecution(r.Context(), workspace.ExecutionPredecessor{JobID: jobID.Load().(domain.ID), ExecutionID: input.ExecutionID}, preparation, manifest)
			if cleanupFailure {
				if err == nil {
					_ = inspection.Close()
					t.Error("changed workspace claim proved cleanup")
				}
			} else if err != nil {
				t.Error("report preceded native/workspace cleanup", err)
				w.WriteHeader(500)
				return
			} else if err := inspection.Close(); err != nil {
				t.Error(err)
			}
		}
		if drop {
			retained := httptest.NewRecorder()
			handler.ServeHTTP(retained, r)
			if retained.Code != http.StatusOK {
				t.Error("fault did not follow committed original receipt", retained.Code)
			}
			http.Error(w, "scripted lost acknowledgment", http.StatusServiceUnavailable)
			return
		}
		handler.ServeHTTP(w, r)
	}))
	defer faultServer.Close()
	credential := worker.Credential{Version: 1, Type: domain.WorkerDevice, Endpoint: faultServer.URL, ServerID: f.identity.ServerID, DeviceID: f.workerDevice, MachineID: f.selection.MachineID, PairingID: domain.NewID(), Token: f.workerIdentity.Token}
	raw, _ := json.Marshal(credential)
	if err := security.WriteAtomic(filepath.Join(f.workerRoot, "device.json"), raw); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	running, stop := context.WithCancel(ctx)
	done, ready := make(chan struct{}), make(chan domain.ID, 1)
	go func() {
		defer close(done)
		_ = worker.Run(running, worker.Config{Root: f.workerRoot, Logger: slog.New(slog.NewJSONHandler(&logs, nil)), Ready: func(id domain.ID) { ready <- id }})
	}()
	defer func() { stop(); <-done }()
	select {
	case <-ready:
	case <-done:
		t.Fatal("Worker failed before readiness")
	case <-ctx.Done():
		t.Fatal("Worker readiness timed out")
	}
	sr := f.refresh(t)
	resumed, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME}))
	if err != nil || resumed.Msg.Change.ExecutionJob == nil {
		t.Fatal("public Grok Resume failed", err)
	}
	assignment := resumed.Msg.Change.ExecutionJob
	var accepted domain.Job
	var input domain.ExecutionJobInput
	if domain.Decode(assignment.DocumentJson, &accepted) != nil || domain.Decode(accepted.Input, &input) != nil || input.Validate() != nil || input.Configuration.GrokContext == nil || *input.Configuration.GrokContext != (domain.GrokModelContext{Tokens: 48000, Source: domain.UserDeclared}) {
		t.Fatal("assignment lost selected context provenance")
	}
	jobID.Store(domain.ID(assignment.Id))
	original.Store(&input)
	close(assignmentReady)
	if cancelled || stopped {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("original input did not reach provider")
		}
		if stopped {
			for {
				changed := f.service.Store.Changed()
				session, err := store.Decode[domain.Session](f.refresh(t))
				if err != nil {
					t.Fatal(err)
				}
				if session.Execution != nil && ((pending && session.Execution.NativeTurnID != "") || (!pending && session.Execution.GrokContent != nil && session.Execution.GrokContent.MessageID != "")) {
					break
				}
				select {
				case <-changed:
				case <-ctx.Done():
					t.Fatal("original text not published")
				}
			}
		}
		sr := f.refresh(t)
		if _, err := sessionClient(f.accountFixture).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_STOP})); err != nil {
			t.Fatal(err)
		}
		select {
		case <-upstreamStopped:
		case <-ctx.Done():
			t.Fatal("cancellation retained original upstream work")
		}
	}
	for {
		changed := f.service.Store.Changed()
		record, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(assignment.Id))
		job, decodeErr := store.Decode[domain.Job](record)
		if err != nil || decodeErr != nil {
			t.Fatal(err, decodeErr)
		}
		if job.State.Terminal() || job.State == domain.JobUncertain {
			if cleanupFailure || cancelled {
				session, err := store.Decode[domain.Session](f.refresh(t))
				if err != nil || job.State != domain.JobUncertain || len(job.Output) != 0 || job.Problem == nil || job.Problem.Code != domain.RecoveryRequired || session.Execution == nil || (session.Execution.GrokTerminal != nil) != (cleanupFailure && !stopped) || stopped && session.Execution.GrokStop == nil || session.Execution.CleanupVerified || session.Recovery == domain.NoRecovery {
					stop()
					<-done
					t.Log(logs.String())
					t.Fatal("workspace failure gained completion authority", job.State, job.Problem)
				}
				break
			}
			var completion domain.ExecutionCompletion
			wantedState := domain.JobSucceeded
			if stopped {
				wantedState = domain.JobCanceled
			}
			if job.State != wantedState || domain.Decode(job.Output, &completion) != nil || completion.ValidateForHarness(domain.GrokBuild) != nil || completion.Version != 1 {
				stop()
				<-done
				t.Log(logs.String())
				t.Fatal("Grok runner failed", job.State, job.Problem)
			}
			session, err := store.Decode[domain.Session](f.refresh(t))
			stopKind := domain.GrokInterruptedText
			if pending {
				stopKind = domain.GrokInterruptedBeforeText
			}
			if err != nil || session.Execution == nil || !session.Execution.CleanupVerified || (session.Execution.GrokTerminal != nil) == stopped || (stopped && (session.Execution.GrokStop == nil || session.Execution.GrokStop.Kind != stopKind || session.Execution.GrokStop.ContextTokens == nil || session.Execution.GrokStop.Completed != nil || session.Execution.Outcome != domain.ExecutionStopped || completion.Outcome != domain.ExecutionStopped || session.Recovery != domain.NoRecovery)) || session.Execution.Observed.GrokContextTokens != 48000 || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID != "" || session.PendingInputs != 0 {
				t.Fatal("completion lost cleanup/context or granted continuation", err)
			}
			if pending && (session.Execution.GrokContent != nil || session.Execution.LatestUsageID != "" || session.Execution.GrokStop.MessageID != "" || session.Execution.GrokStop.TextChunks != 0) {
				t.Fatal("pre-text Stop fabricated output or usage")
			}
			break
		}
		select {
		case <-changed:
		case <-ctx.Done():
			t.Fatal("Grok execution timed out")
		}
	}
	// Join the original report acknowledgment and durable local receipt before
	// stopping the stream. Terminal acknowledgment loss must not replay input.
	for {
		raw, err := security.ReadPrivate(filepath.Join(f.workerRoot, "jobs", assignment.Id+".json"), 2<<20)
		var journal struct {
			State string `json:"state"`
		}
		if err == nil && json.Unmarshal(raw, &journal) == nil && journal.State == "reported" {
			break
		}
		select {
		case <-time.After(10 * time.Millisecond):
		case <-ctx.Done():
			t.Fatal("original report was not retained")
		}
	}
	stop()
	<-done
	if err := process.ReconcileOwner(filepath.Join(f.workerRoot, "processes"), domain.ID(assignment.Id)); err != nil {
		t.Fatal("original native process was not joined", err)
	}
	wantedPublications := uint32(6)
	if stopped {
		wantedPublications = 5
	}
	if pending {
		wantedPublications = 4
	}
	if cancelled {
		wantedPublications = 2
	}
	if publications.Load() != wantedPublications || reports.Load() != 1 || calls.Load() == 0 {
		t.Fatal("receipt loss changed original execution boundaries", publications.Load(), reports.Load(), calls.Load())
	}
	claims, err := security.ReadPrivate(filepath.Join(f.workerRoot, "jobs", assignment.Id, "grok-claims.json"), 256<<10)
	wantedClosure := 1
	if cancelled || stopped {
		wantedClosure = 0
	}
	if err != nil || bytes.Count(claims, []byte(`"phase":"claim-input"`)) != 1 || bytes.Count(claims, []byte(`"phase":"claim-closure"`)) != wantedClosure {
		t.Fatal("original input/closure claims changed", err)
	}
	if stopped && bytes.Count(claims, []byte(`"stop":`)) != 1 {
		t.Fatal("Stop claimed more than once")
	}
	if err := filepath.WalkDir(filepath.Join(f.workerRoot, "runtimes", string(input.ExecutionID)), func(path string, entry os.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if bytes.Contains(raw, []byte(credential.Token)) || bytes.Contains(raw, []byte(apiproxy.TokenPrefix)) {
			t.Error("native runtime retained a credential")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{credential.Token, input.Input.Prompt, f.workerRoot, "Original public Grok completion."} {
		if strings.Contains(logs.String(), private) {
			t.Fatal("runner logs exposed private data")
		}
	}
}
