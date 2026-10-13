// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
	"google.golang.org/protobuf/proto"
)

type startupFaultMode int

const (
	startupFaultNone startupFaultMode = iota
	// The transport fails before the server accepts the request.
	startupFaultBeforeAcceptance
	// The server accepts the request but the response is lost.
	startupFaultAfterAcceptance
	startupFaultRejected
	startupFaultBlocked
)

// startupReplayServer models the server receipt store: one accepted observation
// per request ID, exact replays marked Replayed.
type startupReplayServer struct {
	delidevv1connect.WorkerServiceClient
	mu       sync.Mutex
	fault    startupFaultMode
	faultFor int
	calls    []*pb.ReportExecutionStartupRequest
	accepted map[string]*pb.ExecutionStartupObservation
	events   []string
}

func (s *startupReplayServer) ReportExecutionStartup(ctx context.Context, r *connect.Request[pb.ReportExecutionStartupRequest]) (*connect.Response[pb.ReportExecutionStartupResponse], error) {
	s.mu.Lock()
	call := len(s.calls)
	s.calls = append(s.calls, proto.Clone(r.Msg).(*pb.ReportExecutionStartupRequest))
	s.events = append(s.events, "startup")
	fault := startupFaultNone
	if call < s.faultFor {
		fault = s.fault
	}
	if s.accepted == nil {
		s.accepted = map[string]*pb.ExecutionStartupObservation{}
	}
	s.mu.Unlock()
	switch fault {
	case startupFaultBeforeAcceptance:
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	case startupFaultRejected:
		return nil, connect.NewError(connect.CodePermissionDenied, nil)
	case startupFaultBlocked:
		<-ctx.Done()
		return nil, connect.NewError(connect.CodeUnavailable, ctx.Err())
	}
	s.mu.Lock()
	_, replayed := s.accepted[r.Msg.Mutation.RequestId]
	s.accepted[r.Msg.Mutation.RequestId] = r.Msg.Observation
	s.mu.Unlock()
	if fault == startupFaultAfterAcceptance {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ReportExecutionStartupResponse{Observation: r.Msg.Observation, Replayed: replayed}), nil
}

func (s *startupReplayServer) ReportWork(context.Context, *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, "terminal")
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}

type startupReplayFixture struct {
	server   *startupReplayServer
	config   Config
	c        *PublicationConfig
	job      domain.Job
	jobID    domain.ID
	instance domain.ID
	machine  domain.ID
	logs     *bytes.Buffer
}

func newStartupReplayFixture(t *testing.T, fault startupFaultMode, faultFor int) *startupReplayFixture {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	if err := security.PrivateDir(root); err != nil {
		t.Fatal(err)
	}
	if err := security.PrivateDir(filepath.Join(root, "jobs")); err != nil {
		t.Fatal(err)
	}
	f := &startupReplayFixture{server: &startupReplayServer{fault: fault, faultFor: faultFor}, jobID: domain.NewID(), instance: domain.NewID(), machine: domain.NewID(), logs: &bytes.Buffer{}}
	input := domain.ExecutionJobInput{Version: 4, MachineID: f.machine, Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	f.job = domain.Job{Type: domain.ExecuteSessionJob, State: domain.JobClaimed, MachineID: f.machine, Input: raw}
	f.config = Config{Root: root, Logger: slog.New(slog.NewJSONHandler(f.logs, nil))}
	f.c = &PublicationConfig{Root: root, Credential: Credential{MachineID: f.machine, Token: "fixture-token"}, Instance: f.instance, Assignment: &pb.Resource{Id: string(f.jobID), Revision: 3}, Client: f.server, Logger: f.config.Logger}
	return f
}

// fail drives the production report path with an original no-send failure.
func (f *startupReplayFixture) fail(t *testing.T) error {
	t.Helper()
	config := f.config
	config.execution = f.c
	a := newExecutionStartupAttempt(config, f.jobID, domain.ExecutionJobInput{MachineID: f.machine, Configuration: domain.ExecutionConfiguration{Harness: domain.Codex}})
	a.claimedWorkspace()
	if err := security.PrivateDir(filepath.Join(config.Root, "processes")); err != nil {
		t.Fatal(err)
	}
	if err := security.CreatePrivateDirExclusive(filepath.Join(config.Root, "processes", string(f.jobID))); err != nil {
		t.Fatal(err)
	}
	return a.finish(domain.Fail(domain.NotFound, "Missing executable.", "Install it."))
}

func (f *startupReplayFixture) callCount() int {
	f.server.mu.Lock()
	defer f.server.mu.Unlock()
	return len(f.server.calls)
}

func (f *startupReplayFixture) journalPath(name string) string {
	return startupReportPath(f.config.Root, f.jobID, name)
}

func TestStartupReplayRecoversRequestAfterTransportLossBeforeAcceptance(t *testing.T) {
	f := newStartupReplayFixture(t, startupFaultBeforeAcceptance, 1)
	if err := f.fail(t); err == nil || len(f.server.accepted) != 0 {
		t.Fatalf("first report was not lost before acceptance: %v", err)
	}
	if _, err := os.Stat(startupReportAckPath(f.config.Root, f.jobID, startupReportFailure)); !os.IsNotExist(err) {
		t.Fatal("unaccepted report was acknowledged")
	}
	if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true); got != startupReplayAcknowledged {
		t.Fatalf("replay outcome = %s", got)
	}
	if len(f.server.calls) != 2 || len(f.server.accepted) != 1 {
		t.Fatalf("calls=%d accepted=%d", len(f.server.calls), len(f.server.accepted))
	}
	first, second := f.server.calls[0], f.server.calls[1]
	left, _ := proto.MarshalOptions{Deterministic: true}.Marshal(first)
	right, _ := proto.MarshalOptions{Deterministic: true}.Marshal(second)
	if !bytes.Equal(left, right) || first.Mutation.RequestId != second.Mutation.RequestId {
		t.Fatal("replay changed request bytes or identity")
	}
	if o := f.server.accepted[first.Mutation.RequestId]; o.State != pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_FAILED || o.Cleanup != pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_CONFIRMED || o.InputDelivery != pb.ExecutionStartupInputDelivery_EXECUTION_STARTUP_INPUT_DELIVERY_NOT_SENT {
		t.Fatal("original no-send and cleanup proof was not published")
	}
	if _, err := os.Stat(startupReportAckPath(f.config.Root, f.jobID, startupReportFailure)); err != nil {
		t.Fatal("acknowledgement was not persisted", err)
	}
	// A durable acknowledgement ends reconciliation without another request.
	if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true); got != startupReplayAcknowledged || len(f.server.calls) != 2 {
		t.Fatalf("acknowledged report was sent again: %s %d", got, len(f.server.calls))
	}
}

func TestStartupReplayIsIdempotentAfterLostResponse(t *testing.T) {
	f := newStartupReplayFixture(t, startupFaultAfterAcceptance, 1)
	if err := f.fail(t); err == nil {
		t.Fatal("lost response was not reported")
	}
	if len(f.server.accepted) != 1 {
		t.Fatal("server did not accept the first request")
	}
	if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true); got != startupReplayAcknowledged {
		t.Fatalf("replay outcome = %s", got)
	}
	if len(f.server.calls) != 2 || len(f.server.accepted) != 1 || f.server.calls[0].Mutation.RequestId != f.server.calls[1].Mutation.RequestId {
		t.Fatal("replay created another observation or request identity")
	}
}

func TestStartupReplayStaysUncertainWithoutFabricatedProof(t *testing.T) {
	for name, change := range map[string]func(*startupReplayFixture){
		"server rejection": func(f *startupReplayFixture) { f.server.fault, f.server.faultFor = startupFaultRejected, 99 },
		"changed instance": func(f *startupReplayFixture) { f.c.Instance = domain.NewID() },
		"stale revision":   func(f *startupReplayFixture) { f.c.Assignment.Revision++ },
		"changed machine":  func(f *startupReplayFixture) { f.c.Credential.MachineID = domain.NewID() },
		"malformed journal": func(f *startupReplayFixture) {
			if err := security.WriteAtomic(f.journalPath(startupReportFailure), []byte(`{"request":"bm90IHByb3Rv"}`)); err != nil {
				t.Fatal(err)
			}
		},
		"empty journal": func(f *startupReplayFixture) {
			if err := security.WriteAtomic(f.journalPath(startupReportFailure), []byte(`{}`)); err != nil {
				t.Fatal(err)
			}
		},
		"corrupt ready journal": func(f *startupReplayFixture) {
			if err := security.WriteAtomic(f.journalPath(startupReportReady), []byte(`not json`)); err != nil {
				t.Fatal(err)
			}
		},
		"foreign acknowledgement": func(f *startupReplayFixture) {
			if err := security.WriteAtomic(startupReportAckPath(f.config.Root, f.jobID, startupReportFailure), []byte("deadbeef")); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := newStartupReplayFixture(t, startupFaultBeforeAcceptance, 1)
			_ = f.fail(t)
			before := f.callCount()
			change(f)
			if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true); got != startupReplayUncertain {
				t.Fatalf("outcome = %s", got)
			}
			if name != "server rejection" && f.callCount() != before {
				t.Fatal("invalid ownership still reached the server")
			}
			if len(f.server.accepted) != 0 {
				t.Fatal("proof was fabricated")
			}
		})
	}
	t.Run("successful terminal result contradicts failure report", func(t *testing.T) {
		f := newStartupReplayFixture(t, startupFaultBeforeAcceptance, 1)
		_ = f.fail(t)
		before := f.callCount()
		if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, false); got != startupReplayUncertain || f.callCount() != before {
			t.Fatalf("contradictory terminal result was replayed: %s", got)
		}
	})
	t.Run("ready report is never replayed", func(t *testing.T) {
		f := newStartupReplayFixture(t, startupFaultNone, 0)
		// A reported failure returns the original error, not publication uncertainty.
		if err := f.fail(t); domain.SafeError(err).Code != domain.NotFound {
			t.Fatal(err)
		}
		ready, _, err := readStartupReport(f.config.Root, f.c, f.job, startupReportFailure)
		if err != nil {
			t.Fatal(err)
		}
		ready = proto.Clone(ready).(*pb.ReportExecutionStartupRequest)
		ready.Observation.State = pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_READY
		ready.Observation.ProblemCode, ready.Observation.Cleanup = "", pb.ExecutionStartupCleanup_EXECUTION_STARTUP_CLEANUP_UNSPECIFIED
		ready.Observation.Phase, ready.Observation.Protocol = pb.ExecutionStartupPhase_EXECUTION_STARTUP_PHASE_SETTINGS, string(domain.ProtocolFor(domain.Codex))
		ready.Observation.ExecutableSha256 = "0000000000000000000000000000000000000000000000000000000000000000"
		raw, err := proto.Marshal(ready)
		if err != nil {
			t.Fatal(err)
		}
		if err := writeJSON(f.journalPath(startupReportReady), startupReportJournal{Request: raw}); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(startupReportAckPath(f.config.Root, f.jobID, startupReportFailure)); err != nil {
			t.Fatal(err)
		}
		before := f.callCount()
		if got := reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true); got != startupReplayAcknowledged || f.callCount() != before+1 {
			t.Fatalf("ready journal changed replay: %s %d", got, f.callCount()-before)
		}
		for _, call := range f.server.calls[before:] {
			if call.Observation.State == pb.ExecutionStartupState_EXECUTION_STARTUP_STATE_READY {
				t.Fatal("ready acknowledgement was replayed")
			}
		}
	})
}

func TestStartupReplayShutdownJoinsBoundedReconciliation(t *testing.T) {
	f := newStartupReplayFixture(t, startupFaultBeforeAcceptance, 1)
	_ = f.fail(t)
	f.server.fault, f.server.faultFor = startupFaultBlocked, 99
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan startupReplayOutcome, 1)
	go func() { done <- reconcileExecutionStartupReport(ctx, f.config, f.c, f.job, true) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case got := <-done:
		if got != startupReplayRetry {
			t.Fatalf("canceled outcome = %s", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("shutdown did not join reconciliation")
	}
	if _, err := os.Stat(f.journalPath(startupReportFailure)); err != nil {
		t.Fatal("interrupted reconciliation lost its exact journal")
	}
	if _, err := os.Stat(startupReportAckPath(f.config.Root, f.jobID, startupReportFailure)); !os.IsNotExist(err) {
		t.Fatal("interrupted reconciliation was acknowledged")
	}
}

func TestStartupJournalsArePrivateAndLogsAreSafe(t *testing.T) {
	f := newStartupReplayFixture(t, startupFaultBeforeAcceptance, 1)
	_ = f.fail(t)
	reconcileExecutionStartupReport(context.Background(), f.config, f.c, f.job, true)
	if runtime.GOOS != "windows" {
		for _, path := range []string{f.journalPath(startupReportFailure), startupReportAckPath(f.config.Root, f.jobID, startupReportFailure)} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != 0600 {
				t.Fatalf("%s is not private: %v %v", filepath.Base(path), info, err)
			}
		}
	}
	for _, secret := range []string{"fixture-token", f.config.Root} {
		if secret != "" && bytes.Contains(f.logs.Bytes(), []byte(secret)) {
			t.Fatalf("log exposed %q", secret)
		}
	}
}

func TestRunAndReportJobPublishesStartupFailureBeforeTerminalResult(t *testing.T) {
	for _, scenario := range []string{"transport loss before acceptance", "response lost after acceptance", "transport still down"} {
		t.Run(scenario, func(t *testing.T) {
			f := newCheckpointFixture(t)
			f.input.Version, f.input.Installation = 4, domain.Installation{}
			f.input.Startup = &domain.ExecutionStartupSelection{Harness: domain.Codex, ExplicitPath: filepath.Join(f.root, "not-installed")}
			raw, err := json.Marshal(f.input)
			if err != nil {
				t.Fatal(err)
			}
			f.job.Input = raw
			f.job.InstanceID = domain.NewID()
			document, err := json.Marshal(f.job)
			if err != nil {
				t.Fatal(err)
			}
			fault := startupFaultBeforeAcceptance
			if scenario == "response lost after acceptance" {
				fault = startupFaultAfterAcceptance
			}
			server := &startupReplayServer{fault: fault, faultFor: 1}
			if scenario == "transport still down" {
				server.faultFor = 99
			}
			if err := security.PrivateDir(filepath.Join(f.root, "jobs")); err != nil {
				t.Fatal(err)
			}
			resource := &pb.Resource{Id: string(f.jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, SchemaVersion: 1, Revision: 2, SessionId: string(f.input.SessionID), DocumentJson: document}
			credential := Credential{MachineID: f.input.MachineID, Token: "fixture-token"}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			config := Config{Root: f.root, Logger: slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil))}
			err = runAndReportJob(ctx, config, server, credential, f.job.InstanceID, assignment{context: ctx, cancel: func() {}}, resource, f.job)
			if scenario == "transport still down" {
				// The exact journal stays for the next attempt; no terminal result is published.
				if err == nil || slices.Contains(server.events, "terminal") {
					t.Fatalf("terminal result was published without startup proof: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(server.events) != 3 || server.events[0] != "startup" || server.events[1] != "startup" || server.events[2] != "terminal" {
				t.Fatalf("terminal result was not ordered after startup replay: %v", server.events)
			}
			if len(server.accepted) != 1 || server.calls[0].Mutation.RequestId != server.calls[1].Mutation.RequestId {
				t.Fatal("replay changed request identity")
			}
		})
	}
}
