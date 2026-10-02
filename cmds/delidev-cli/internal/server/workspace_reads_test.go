package server

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

type workspaceClientReply struct {
	response *connect.Response[pb.ReadSessionWorkspaceResponse]
	err      error
}

func startWorkspaceRead(t *testing.T, ctx context.Context, f *firstDispatchFixture, query domain.WorkspaceReadQuery) <-chan workspaceClientReply {
	t.Helper()
	raw, _ := json.Marshal(query)
	done := make(chan workspaceClientReply, 1)
	go func() {
		value, err := sessionClient(f.accountFixture).ReadSessionWorkspace(ctx, ownerRequest(f.identity, &pb.ReadSessionWorkspaceRequest{SessionId: f.change.Session.Id, QueryJson: raw}))
		done <- workspaceClientReply{value, err}
	}()
	return done
}

func TestWorkspaceReadRelayWhileExecutionRemainsClaimed(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	// Claim the durable native assignment without resolving it. The independent
	// file stream must remain usable while this job blocks subsequent work.
	var execution *pb.Resource
	for f.workerStream.Receive() {
		// Setup can leave a periodic liveness frame ahead of the assignment.
		// Only the actual job establishes the durable work tested below.
		if f.workerStream.Msg().Heartbeat {
			continue
		}
		execution = f.workerStream.Msg().Job
		break
	}
	if execution == nil {
		t.Fatal("missing native assignment", f.workerStream.Err())
	}
	stream, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("missing read readiness", stream.Err())
	}
	done := startWorkspaceRead(t, ctx, f, domain.WorkspaceReadQuery{Operation: domain.WorkspaceFile, Path: "note.txt"})
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var request workspace.ReadRequest
	if err := domain.Decode(stream.Msg().RequestJson, &request); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(request.Manifest.PrimaryPath, "note.txt"), []byte("remote-only file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	busy := <-startWorkspaceRead(t, ctx, f, domain.WorkspaceReadQuery{Operation: domain.WorkspaceDirectory, Path: "."})
	if connect.CodeOf(busy.err) != connect.CodeResourceExhausted {
		t.Fatal("unbounded read concurrency", busy.err)
	}
	m := workspace.Manager{Root: f.workerRoot}
	result, err := m.ReadWorkspace(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(result)
	report := &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(request.ID), DocumentJson: raw}
	wrong := proto.Clone(report).(*pb.ReportWorkspaceReadRequest)
	wrong.ReadId = string(domain.NewID())
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, wrong)); err == nil {
		t.Fatal("foreign read ID accepted")
	}
	malformed := proto.Clone(report).(*pb.ReportWorkspaceReadRequest)
	malformed.DocumentJson = []byte(`{"text":"unvalidated","size":"-1"}`)
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, malformed)); err == nil {
		t.Fatal("malformed content accepted")
	}
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err != nil {
		t.Fatal(err)
	}
	reply := <-done
	if reply.err != nil || domain.Decode(reply.response.Msg.DocumentJson, &result) != nil || result.Text != "remote-only file\n" {
		t.Fatal("lost remote content", reply.err)
	}
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, report)); err == nil {
		t.Fatal("duplicate read response accepted")
	}
	row, err := f.service.Store.Get(ctx, domain.JobKind, domain.ID(execution.Id))
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](row)
	if err != nil || job.State != domain.JobClaimed || row.Revision != execution.Revision {
		t.Fatal("read resolved or changed native work", err)
	}
}

func TestWorkspaceReadRejectsWorkerClientAndPrimaryDisconnect(t *testing.T) {
	f := newFirstDispatchFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	raw, _ := json.Marshal(domain.WorkspaceReadQuery{Operation: domain.WorkspaceRoots})
	if _, err := sessionClient(f.accountFixture).ReadSessionWorkspace(ctx, ownerRequest(f.workerIdentity, &pb.ReadSessionWorkspaceRequest{SessionId: f.change.Session.Id, QueryJson: raw})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker read client data", err)
	}
	roots := <-startWorkspaceRead(t, ctx, f, domain.WorkspaceReadQuery{Operation: domain.WorkspaceRoots})
	if roots.err != nil {
		t.Fatal(roots.err)
	}
	var result domain.WorkspaceReadResult
	if domain.Decode(roots.response.Msg.DocumentJson, &result) != nil || len(result.Roots) != 1 || !result.Roots[0].Primary || result.Roots[0].RepositoryID != "" {
		t.Fatal("invalid projectless root")
	}
	stream, err := f.workerClient.WatchWorkspaceReads(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkspaceReadsRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	done := startWorkspaceRead(t, ctx, f, domain.WorkspaceReadQuery{Operation: domain.WorkspaceDirectory, Path: "."})
	if !stream.Receive() {
		t.Fatal(stream.Err())
	}
	var request workspace.ReadRequest
	if err := domain.Decode(stream.Msg().RequestJson, &request); err != nil {
		t.Fatal(err)
	}
	if err := f.workerStream.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case reply := <-done:
		if reply.err == nil {
			t.Fatal("disconnected primary released file content")
		}
	case <-ctx.Done():
		t.Fatal("pending read did not observe primary loss")
	}
	raw, _ = json.Marshal(domain.WorkspaceReadResult{})
	if _, err := f.workerClient.ReportWorkspaceRead(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkspaceReadRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance, ReadId: string(request.ID), DocumentJson: raw})); err == nil {
		t.Fatal("late response accepted")
	}
}
