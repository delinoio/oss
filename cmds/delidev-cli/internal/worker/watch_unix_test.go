//go:build darwin || linux

package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type terminatingWorkStream struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	job     *pb.Resource
	release <-chan struct{}
	problem error
	reports atomic.Int32
}

func (s *terminatingWorkStream) WatchWork(ctx context.Context, req *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	if err := stream.Send(&pb.WatchWorkResponse{Job: s.job}); err != nil {
		return err
	}
	select {
	case <-s.release:
		return s.problem
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (s *terminatingWorkStream) ReportWork(context.Context, *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	s.reports.Add(1)
	return nil, connect.NewError(connect.CodeInternal, nil)
}

func TestStreamTerminationCancelsRunningOwnedWork(t *testing.T) {
	for _, code := range []connect.Code{connect.CodePermissionDenied, connect.CodeUnavailable, 0, connect.CodeDeadlineExceeded} {
		t.Run(code.String(), func(t *testing.T) {
			root, repo, bin := filepath.Join(t.TempDir(), "worker"), t.TempDir(), t.TempDir()
			for _, name := range []string{"jobs", "empty-hooks"} {
				if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			// This controlled executable reaches the same owned subprocess boundary
			// as Git. It cannot finish naturally within the test deadline.
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf started > native-started\nwhile :; do /bin/sleep 1; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			instance, machine, jobID := domain.NewID(), domain.NewID(), domain.NewID()
			input, _ := json.Marshal(domain.RepositoryInspectionInput{Path: repo})
			job := domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()}
			raw, _ := json.Marshal(job)
			release := make(chan struct{})
			service := &terminatingWorkStream{job: &pb.Resource{Id: string(jobID), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 2, SchemaVersion: 1, DocumentJson: raw}, release: release}
			if code != 0 {
				domainCode := domain.Unavailable
				if code == connect.CodePermissionDenied {
					domainCode = domain.PermissionDenied
				}
				service.problem = rpc.Error(domain.Fail(domainCode, "The fixture stream ended.", ""), string(domain.NewID()))
			}
			_, handler := delidevv1connect.NewWorkerServiceHandler(service)
			server := httptest.NewServer(handler)
			defer server.Close()
			client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				timeout := domain.WorkerConnectionTimeout
				if code == connect.CodeDeadlineExceeded {
					timeout = 3 * time.Second
				}
				done <- watchWithTimeout(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, client, Credential{MachineID: machine, Token: "private-fixture"}, instance, timeout)
			}()
			started := time.Now().Add(5 * time.Second)
			for {
				if _, err := os.Stat(filepath.Join(repo, "native-started")); err == nil {
					break
				}
				if time.Now().After(started) {
					t.Fatal("owned operation never started")
				}
				time.Sleep(10 * time.Millisecond)
			}
			if code != connect.CodeDeadlineExceeded {
				close(release)
			}
			select {
			case err := <-done:
				want := domain.Unavailable
				if code == connect.CodePermissionDenied {
					want = domain.PermissionDenied
				}
				var actual domain.Code
				if typed, ok := err.(*domain.Error); ok {
					actual = typed.Code
				} else {
					actual = rpc.ClientError(err).Code
				}
				if actual != want {
					t.Fatalf("stream termination lost classification: %v", err)
				}
			case <-time.After(5 * time.Second):
				cancel()
				<-done
				t.Fatal("stream termination left native work running")
			}
			if service.reports.Load() != 0 {
				t.Fatal("disconnected work reported success")
			}
			raw, err := security.ReadPrivate(filepath.Join(root, "jobs", string(jobID)+".json"), 2<<20)
			if err != nil {
				t.Fatal(err)
			}
			var result journal
			if err := domain.Decode(raw, &result); err != nil {
				t.Fatal(err)
			}
			if result.State != journalFinished || result.Problem == nil || result.Problem.Code != domain.Canceled || len(result.Output) != 0 {
				t.Fatalf("cancellation not durably retained: %+v", result)
			}
		})
	}
}

type cancelingWorkStream struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	first, second *pb.Resource
	release       <-chan struct{}
	precanceled   bool
	reports       chan *pb.ReportWorkRequest
	observed      chan *pb.ReportWorkRequest
}

func (s *cancelingWorkStream) WatchWork(ctx context.Context, _ *connect.Request[pb.WatchWorkRequest], stream *connect.ServerStream[pb.WatchWorkResponse]) error {
	if err := stream.Send(&pb.WatchWorkResponse{Job: s.first, CancelRequested: s.precanceled}); err != nil {
		return err
	}
	if !s.precanceled {
		select {
		case <-s.release:
		case <-ctx.Done():
			return ctx.Err()
		}
		if err := stream.Send(&pb.WatchWorkResponse{CancelJobId: s.first.Id}); err != nil {
			return err
		}
	}
	select {
	case <-s.reports:
	case <-ctx.Done():
		return ctx.Err()
	}
	if err := stream.Send(&pb.WatchWorkResponse{Job: s.second}); err != nil {
		return err
	}
	// A delayed control for the previous assignment must not cancel the next one.
	if err := stream.Send(&pb.WatchWorkResponse{CancelJobId: s.first.Id}); err != nil {
		return err
	}
	<-ctx.Done()
	return ctx.Err()
}
func (s *cancelingWorkStream) ReportWork(_ context.Context, req *connect.Request[pb.ReportWorkRequest]) (*connect.Response[pb.ReportWorkResponse], error) {
	s.reports <- req.Msg
	s.observed <- req.Msg
	return connect.NewResponse(&pb.ReportWorkResponse{}), nil
}
func TestTargetedCancellationKeepsStreamAndNextOperationUsable(t *testing.T) {
	for _, precanceled := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "before-start"}[precanceled], func(t *testing.T) {
			root, repo, bin := filepath.Join(t.TempDir(), "worker"), t.TempDir(), t.TempDir()
			for _, name := range []string{"jobs", "empty-hooks"} {
				if err := security.PrivateDir(filepath.Join(root, name)); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.WriteFile(filepath.Join(bin, "git"), []byte("#!/bin/sh\nprintf started > native-started\nwhile :; do /bin/sleep 1; done\n"), 0700); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
			instance, machine, firstID, secondID, session := domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID(), domain.NewID()
			input, _ := json.Marshal(domain.RepositoryInspectionInput{Path: repo})
			firstRaw, _ := json.Marshal(domain.Job{Type: domain.InspectRepositoryJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()})
			input, _ = json.Marshal(workspace.PrepareRequest{SessionID: session, MachineID: machine, Type: domain.GeneralChat, Repositories: []workspace.RepositorySpec{}})
			secondRaw, _ := json.Marshal(domain.Job{Type: domain.PrepareWorkspaceJob, State: domain.JobClaimed, MachineID: machine, InstanceID: instance, Input: input, AcceptedAt: time.Now().UTC()})
			release := make(chan struct{})
			service := &cancelingWorkStream{
				first:   &pb.Resource{Id: string(firstID), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 2, SchemaVersion: 1, DocumentJson: firstRaw},
				second:  &pb.Resource{Id: string(secondID), SessionId: string(session), Kind: pb.EntityKind_ENTITY_KIND_JOB, Revision: 2, SchemaVersion: 1, DocumentJson: secondRaw},
				release: release, precanceled: precanceled, reports: make(chan *pb.ReportWorkRequest, 2), observed: make(chan *pb.ReportWorkRequest, 2),
			}
			_, handler := delidevv1connect.NewWorkerServiceHandler(service)
			server := httptest.NewServer(handler)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			done := make(chan error, 1)
			go func() {
				done <- watch(ctx, Config{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}, delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL), Credential{MachineID: machine, Token: "private-fixture"}, instance)
			}()
			defer func() { cancel(); <-done }()
			if !precanceled {
				until := time.Now().Add(4 * time.Second)
				for {
					if _, err := os.Stat(filepath.Join(repo, "native-started")); err == nil {
						break
					}
					if time.Now().After(until) {
						t.Fatal("native work never started")
					}
					time.Sleep(10 * time.Millisecond)
				}
				close(release)
			}
			reports := []*pb.ReportWorkRequest{}
			for len(reports) < 2 {
				select {
				case report := <-service.observed:
					reports = append(reports, report)
				case <-ctx.Done():
					t.Fatal("cancellation ended the stream or failed to stop work")
				}
			}
			if reports[0].Mutation.Id != string(firstID) || reports[0].Problem == nil || reports[0].Problem.Code != string(domain.Canceled) || len(reports[0].OutputJson) != 0 {
				t.Fatal("cancellation was not reported for the exact job")
			}
			if reports[1].Mutation.Id != string(secondID) || reports[1].Problem != nil {
				t.Fatalf("next job failed after scoped cancellation: %v", reports[1].Problem)
			}
			var result workspace.Manifest
			if err := domain.Decode(reports[1].OutputJson, &result); err != nil || result.State != workspace.Ready || result.SessionID != session {
				t.Fatal("next job did not prepare its workspace")
			}
			if precanceled {
				if _, err := os.Stat(filepath.Join(repo, "native-started")); !os.IsNotExist(err) {
					t.Fatal("precanceled assignment started a native process")
				}
			}
		})
	}
}
