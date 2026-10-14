// SPDX-License-Identifier: Apache-2.0
//go:build darwin || linux

package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

// parallelStream sends session preparation assignments and gates their reports.
type parallelStream struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	jobs []*pb.Resource
	// sequential sends the next assignment only after the previous report
	// returned, as a server that did not negotiate parallel sessions does.
	sequential bool
	// holdReports blocks each report until this many reports are concurrently
	// in flight, proving that no assignment waits for an earlier one to settle.
	holdReports int
	// lateControl is sent for the first job after its report returned.
	lateControl bool
	// unknownControl makes the stream send a control for a never-assigned job.
	unknownControl bool
	// endAfterAssign ends the stream with an error once every job was sent.
	endAfterAssign bool

	mu         sync.Mutex
	inFlight   int
	released   bool
	maxFlight  int
	reported   []string
	reportDone chan string
	waiting    chan struct{}
}

func (s *parallelStream) WatchWork(ctx context.Context, req *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	for _, job := range s.jobs {
		if err := stream.Send(&pb.WatchWorkResponse{Job: job}); err != nil {
			return err
		}
		if s.sequential {
			select {
			case <-s.reportDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	if s.endAfterAssign {
		<-s.waiting
		return connect.NewError(connect.CodeUnavailable, nil)
	}
	if !s.sequential {
		for range s.jobs {
			select {
			case <-s.reportDone:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
	// Let each assignment write its final journal and retire its live context
	// before the stream ends or a late control arrives.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return ctx.Err()
	}
	if s.lateControl {
		first := s.jobs[0]
		if err := stream.Send(&pb.WatchWorkResponse{QuestionResponse: &pb.QuestionResponseControl{JobId: first.Id, InteractionId: string(domain.NewID()), ResponseId: string(domain.NewID()), Revision: 1}}); err != nil {
			return err
		}
		// The late control for a completed job must leave the stream alive.
		select {
		case <-time.After(300 * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if s.unknownControl {
		return stream.Send(&pb.WatchWorkResponse{QuestionResponse: &pb.QuestionResponseControl{JobId: string(domain.NewID()), InteractionId: string(domain.NewID()), ResponseId: string(domain.NewID()), Revision: 1}})
	}
	return connect.NewError(connect.CodeUnavailable, nil)
}

func (s *parallelStream) ReportWork(ctx context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	s.mu.Lock()
	s.inFlight++
	s.maxFlight = max(s.maxFlight, s.inFlight)
	arrived := s.inFlight
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		s.inFlight--
		s.mu.Unlock()
	}()
	if s.endAfterAssign {
		if arrived == len(s.jobs) {
			close(s.waiting)
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if s.holdReports > 0 {
		deadline := time.After(10 * time.Second)
		for {
			s.mu.Lock()
			if s.inFlight >= s.holdReports {
				s.released = true
			}
			released := s.released
			s.mu.Unlock()
			if released {
				break
			}
			select {
			case <-time.After(5 * time.Millisecond):
			case <-deadline:
				return nil, connect.NewError(connect.CodeDeadlineExceeded, nil)
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}
	s.mu.Lock()
	s.reported = append(s.reported, req.Msg.Mutation.Id)
	s.mu.Unlock()
	s.reportDone <- req.Msg.Mutation.Id
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}

func parallelPreparations(t *testing.T, machine, instance domain.ID, count int) []*pb.Resource {
	t.Helper()
	resources := []*pb.Resource{}
	for range count {
		session := domain.NewID()
		input, _ := json.Marshal(workspace.PrepareRequest{SessionID: session, MachineID: machine, Type: domain.GeneralChat})
		job := domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()}
		raw, _ := json.Marshal(job)
		resources = append(resources, &pb.Resource{Id: string(domain.NewID()), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 2, SchemaVersion: 1, SessionId: string(session), DocumentJson: raw})
	}
	return resources
}

func runParallelStream(t *testing.T, service *parallelStream, parallel bool, machine, instance domain.ID, timeout time.Duration) (error, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "worker")
	for _, name := range []string{"jobs", "empty-hooks"} {
		if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	config := Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil)), parallelSessions: parallel}
	return watchWithTimeout(ctx, config, client, Credential{MachineID: machine, Token: "private-fixture"}, instance, domain.WorkerConnectionTimeout), root
}

// requireStreamUnavailable fails unless the stream ended with the fixture's
// Unavailable status, so a nil error, a different Connect code or a different
// domain code can never pass as the expected stream end.
func requireStreamUnavailable(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("stream end was not reported")
	}
	var typed *domain.Error
	if errors.As(err, &typed) {
		if typed.Code != domain.Unavailable {
			t.Fatal("stream ended with an unexpected domain code", err)
		}
		return
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("stream ended with an unexpected error", err)
	}
}

func TestParallelSessionAssignmentsRunConcurrentlyAndReportOutOfOrder(t *testing.T) {
	machine, instance := domain.NewID(), domain.NewID()
	// Eight independent sessions exceed any fixed small ceiling. Each report
	// blocks until all eight are in flight, which a serial executor can never do.
	service := &parallelStream{jobs: parallelPreparations(t, machine, instance, 8), holdReports: 8, reportDone: make(chan string, 16)}
	err, root := runParallelStream(t, service, true, machine, instance, 90*time.Second)
	requireStreamUnavailable(t, err)
	if service.maxFlight != 8 || len(service.reported) != 8 {
		t.Fatalf("assignments did not run concurrently: max=%d reported=%d", service.maxFlight, len(service.reported))
	}
	for _, job := range service.jobs {
		raw, readErr := security.ReadPrivate(filepath.Join(root, "jobs", job.Id+".json"), 2<<20)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var result journal
		if domain.Decode(raw, &result) != nil || result.JobID != domain.ID(job.Id) || result.State != journalReported {
			t.Fatal("a job journal crossed identities or was not reported", job.Id)
		}
	}
}

func TestSerialStreamKeepsOneAssignmentAtATime(t *testing.T) {
	machine, instance := domain.NewID(), domain.NewID()
	service := &parallelStream{jobs: parallelPreparations(t, machine, instance, 3), sequential: true, reportDone: make(chan string, 16)}
	err, _ := runParallelStream(t, service, false, machine, instance, 90*time.Second)
	requireStreamUnavailable(t, err)
	if service.maxFlight != 1 || len(service.reported) != 3 {
		t.Fatalf("legacy stream overlapped assignments: max=%d reported=%d", service.maxFlight, len(service.reported))
	}
}

func TestLateControlForCompletedJobResolvesAgainstOriginalReceipt(t *testing.T) {
	machine, instance := domain.NewID(), domain.NewID()
	service := &parallelStream{jobs: parallelPreparations(t, machine, instance, 3), holdReports: 3, lateControl: true, reportDone: make(chan string, 16)}
	err, _ := runParallelStream(t, service, true, machine, instance, 90*time.Second)
	// The stream ends only through the fixture's Unavailable status, never through
	// a protocol failure raised by the late control for the completed first job.
	requireStreamUnavailable(t, err)
	if len(service.reported) != 3 {
		t.Fatal("jobs did not finish before the late control", len(service.reported))
	}
}

func TestControlForNeverAssignedJobFailsTheStream(t *testing.T) {
	machine, instance := domain.NewID(), domain.NewID()
	service := &parallelStream{jobs: parallelPreparations(t, machine, instance, 1), holdReports: 1, unknownControl: true, reportDone: make(chan string, 16)}
	err, _ := runParallelStream(t, service, true, machine, instance, 90*time.Second)
	var typed *domain.Error
	if !errors.As(err, &typed) || typed.Code != publicationUncertain().Code {
		t.Fatal("an unknown job control was accepted", err)
	}
}

func TestStreamLossJoinsEveryOwnedParallelAssignment(t *testing.T) {
	machine, instance := domain.NewID(), domain.NewID()
	service := &parallelStream{jobs: parallelPreparations(t, machine, instance, 4), endAfterAssign: true, waiting: make(chan struct{}), reportDone: make(chan string, 16)}
	done := make(chan struct{})
	var streamErr error
	go func() {
		defer close(done)
		streamErr, _ = runParallelStream(t, service, true, machine, instance, 90*time.Second)
	}()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		t.Fatal("stream loss left parallel assignments running")
	}
	requireStreamUnavailable(t, streamErr)
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.inFlight != 0 {
		t.Fatal("an owned assignment outlived the stream", service.inFlight)
	}
}
