package worker

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

type workspaceReadTransport struct {
	delidevv1connect.UnimplementedWorkerServiceHandler
	requests []workspace.ReadRequest
	reports  chan *pb.ReportWorkspaceReadRequest
}

func (s *workspaceReadTransport) WatchWorkspaceReads(ctx context.Context, req *connect.Request[pb.WatchWorkspaceReadsRequest], stream *connect.ServerStream[pb.WatchWorkspaceReadsResponse]) error {
	if req.Header().Get("Authorization") != "Bearer fixture-token" {
		return connect.NewError(connect.CodeUnauthenticated, nil)
	}
	if err := stream.Send(&pb.WatchWorkspaceReadsResponse{Heartbeat: true}); err != nil {
		return err
	}
	for _, request := range s.requests {
		raw, _ := json.Marshal(request)
		if err := stream.Send(&pb.WatchWorkspaceReadsResponse{RequestJson: raw}); err != nil {
			return err
		}
	}
	<-ctx.Done()
	return nil
}
func (s *workspaceReadTransport) ReportWorkspaceRead(_ context.Context, req *connect.Request[pb.ReportWorkspaceReadRequest]) (*connect.Response[pb.ReportWorkspaceReadResponse], error) {
	s.reports <- req.Msg
	if req.Msg.ReadId == string(s.requests[0].ID) {
		return nil, connect.NewError(connect.CodeUnavailable, nil)
	}
	return connect.NewResponse(&pb.ReportWorkspaceReadResponse{}), nil
}

func TestWorkspaceReaderDiscardsCanceledObservationWithoutDisconnectingNextRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	manager := workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := manager.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "file.txt"), []byte("original workspace content"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &workspaceReadTransport{reports: make(chan *pb.ReportWorkspaceReadRequest, 2)}
	for range 2 {
		service.requests = append(service.requests, workspace.ReadRequest{ID: domain.NewID(), Deadline: time.Now().Add(15 * time.Second), Preparation: input, Manifest: manifest, Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "file.txt"}})
	}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
	done := make(chan error, 1)
	go func() {
		done <- receiveWorkspaceReads(ctx, Config{Root: root, Logger: manager.Logger}, client, Credential{MachineID: input.MachineID, Token: "fixture-token"}, domain.NewID())
	}()
	for i := range 2 {
		select {
		case report := <-service.reports:
			var value domain.WorkspaceReadResult
			if report.ReadId != string(service.requests[i].ID) || report.ProblemCode != "" || domain.Decode(report.DocumentJson, &value) != nil || value.Text != "original workspace content" {
				t.Fatal("observation content or scope lost")
			}
		case <-ctx.Done():
			t.Fatal("canceled report prevented the next read")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not join cancellation")
	}
}

func TestWorkspaceReaderNeverFallsBackFromPrivatePRProfileToFileRead(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	root := filepath.Join(t.TempDir(), "worker")
	manager := workspace.Manager{Root: root, Logger: slog.New(slog.NewJSONHandler(io.Discard, nil))}
	input := workspace.PrepareRequest{SessionID: domain.NewID(), MachineID: domain.NewID(), Type: domain.GeneralChat}
	manifest, err := manager.Prepare(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(manifest.PrimaryPath, "file.txt"), []byte("ordinary explicit file view"), 0600); err != nil {
		t.Fatal(err)
	}
	service := &workspaceReadTransport{reports: make(chan *pb.ReportWorkspaceReadRequest, 2)}
	for range 2 {
		service.requests = append(service.requests, workspace.ReadRequest{ID: domain.NewID(), Deadline: time.Now().UTC().Add(15 * time.Second), Preparation: input, Manifest: manifest, Query: domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "file.txt"}})
	}
	// A malformed mixed request must not fall back to its otherwise valid
	// public file query. The next independent ordinary read remains usable.
	service.requests[0].PRCandidate = &domain.PRGitTarget{}
	_, handler := delidevv1connect.NewWorkerServiceHandler(service)
	server := httptest.NewServer(handler)
	defer server.Close()
	client := delidevv1connect.NewWorkerServiceClient(server.Client(), server.URL)
	done := make(chan error, 1)
	go func() {
		done <- receiveWorkspaceReads(ctx, Config{Root: root, Logger: manager.Logger}, client, Credential{MachineID: input.MachineID, Token: "fixture-token"}, domain.NewID())
	}()
	for i := range 2 {
		select {
		case report := <-service.reports:
			if report.ReadId != string(service.requests[i].ID) {
				t.Fatal("read ownership changed")
			}
			if i == 0 {
				if report.ProblemCode != string(domain.Unsupported) || len(report.DocumentJson) != 0 {
					t.Fatal("private profile was downgraded to file content")
				}
			} else {
				var result domain.WorkspaceReadResult
				if report.ProblemCode != "" || domain.Decode(report.DocumentJson, &result) != nil || result.Text != "ordinary explicit file view" {
					t.Fatal("independent file view was affected")
				}
			}
		case <-ctx.Done():
			t.Fatal("reader did not settle original and subsequent requests")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("reader did not join cancellation")
	}
}
