package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func workspaceStream(t *testing.T, f *accountFixture, identity security.Identity, machine domain.ID) (context.Context, delidevv1connect.WorkerServiceClient, string, *connect.ServerStreamForClient[pb.WatchWorkResponse]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	client := delidevv1connect.NewWorkerServiceClient(http.DefaultClient, f.endpoint.URL)
	instance := string(domain.NewID())
	if _, err := client.AttachWorker(ctx, ownerRequest(identity, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(machine), InstanceId: instance, Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	stream, err := client.WatchWork(ctx, ownerRequest(identity, &pb.WatchWorkRequest{MachineId: string(machine), InstanceId: instance}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	if !stream.Receive() || !stream.Msg().Heartbeat {
		t.Fatal("no stream readiness", stream.Err())
	}
	return ctx, client, instance, stream
}

func TestWorkspaceArchiveCancellationPreservesAssignmentAndNextJob(t *testing.T) {
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	_, first := createSessionFixture(t, f, selection)
	_, second := createSessionFixture(t, f, selection)
	ctx, worker, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("no first assignment", stream.Err())
	}
	claimed := stream.Msg().Job
	if claimed.Id != first.WorkspaceJob.Id {
		t.Fatal("wrong first assignment")
	}
	before := currentCatalogResource(t, f, claimed)
	archive := &pb.ControlSessionRequest{Mutation: acctMutation(first.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}
	response, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, archive))
	if err != nil {
		t.Fatal(err)
	}
	v := sessionBody(t, response.Msg.Change.Session)
	if v.Archive != domain.ArchivePending || v.Dispatch != domain.DispatchPaused || v.Preparation.State != domain.PreparationStopping {
		t.Fatalf("unconfirmed native cleanup claimed complete: %+v", v)
	}
	if !stream.Receive() || stream.Msg().CancelJobId != claimed.Id || stream.Msg().Job != nil {
		t.Fatal("missing scoped cancellation", stream.Err())
	}
	after := currentCatalogResource(t, f, claimed)
	if after.Revision != before.Revision || !bytes.Equal(after.DocumentJson, before.DocumentJson) {
		t.Fatal("cancellation invalidated the journal envelope")
	}
	pending := currentCatalogResource(t, f, second.WorkspaceJob)
	var job domain.Job
	if err := domain.Decode(pending.DocumentJson, &job); err != nil || job.State != domain.JobQueued {
		t.Fatal("cancellation preclaimed unrelated work")
	}
	report := &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}}
	if _, err := worker.ReportWork(ctx, ownerRequest(identity, report)); err != nil {
		t.Fatal(err)
	}
	finalized := sessionBody(t, currentCatalogResource(t, f, first.Session))
	if finalized.Archive != domain.Archived || finalized.Preparation.State != domain.PreparationCanceled || finalized.Recovery != domain.NoRecovery || finalized.Outcome != domain.ExecutionNotStarted {
		t.Fatalf("wrong completion: %+v", finalized)
	}
	if !stream.Receive() || stream.Msg().Job == nil || stream.Msg().Job.Id != second.WorkspaceJob.Id || stream.Msg().CancelRequested {
		t.Fatal("next job inherited cancellation", stream.Err())
	}
	if _, err := worker.ReportWork(ctx, ownerRequest(identity, report)); err != nil {
		t.Fatal("exact report retry failed", err)
	}
	replay, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, archive))
	if err != nil || !replay.Msg.Change.Replayed || sessionBody(t, replay.Msg.Change.Session).Archive != domain.Archived {
		t.Fatal("archive receipt did not resolve current state", err)
	}
}

func TestWorkspaceQueuedCancellationRetryAndRequestRetention(t *testing.T) {
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	stopped, err := sessionClient(f).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(initial.Session, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_STOP}))
	if err != nil {
		t.Fatal(err)
	}
	var canceled domain.Job
	if err := domain.Decode(stopped.Msg.Change.WorkspaceJob.DocumentJson, &canceled); err != nil || canceled.State != domain.JobCanceled || canceled.InstanceID != "" {
		t.Fatal("queued work was not canceled before native claim")
	}
	retry := &pb.PrepareSessionWorkspaceRequest{Mutation: acctMutation(stopped.Msg.Change.Session, domain.NewID())}
	if _, err := sessionClient(f).PrepareSessionWorkspace(context.Background(), ownerRequest(identity, retry)); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker prepared owner session")
	}
	prepared, err := sessionClient(f).PrepareSessionWorkspace(context.Background(), ownerRequest(f.identity, retry))
	if err != nil {
		t.Fatal(err)
	}
	var next domain.Job
	if err := domain.Decode(prepared.Msg.Change.WorkspaceJob.DocumentJson, &next); err != nil {
		t.Fatal(err)
	}
	if next.State != domain.JobQueued || prepared.Msg.Change.WorkspaceJob.Id == initial.WorkspaceJob.Id || !bytes.Equal(next.Input, canceled.Input) || sessionBody(t, prepared.Msg.Change.Session).Dispatch != domain.DispatchPaused {
		t.Fatal("retry changed accepted workspace or resumed input")
	}
	f.shutdown()
	f.start()
	replay, err := sessionClient(f).PrepareSessionWorkspace(context.Background(), ownerRequest(f.identity, retry))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.WorkspaceJob.Id != prepared.Msg.Change.WorkspaceJob.Id {
		t.Fatal("retry duplicated after restart", err)
	}
}

func TestWorkspaceMalformedSuccessRequiresRecoveryAndCannotArchive(t *testing.T) {
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	_, initial := createSessionFixture(t, f, selection)
	ctx, worker, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal(stream.Err())
	}
	claimed := stream.Msg().Job
	raw, _ := json.Marshal(map[string]string{"primary_path": "/untrusted"})
	report := &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, OutputJson: raw}
	response, err := worker.ReportWork(ctx, ownerRequest(identity, report))
	if err != nil {
		t.Fatal(err)
	}
	var job domain.Job
	if err := domain.Decode(response.Msg.Job.DocumentJson, &job); err != nil || job.State != domain.JobUncertain || len(job.Output) != 0 {
		t.Fatal("unproven output published")
	}
	current := currentCatalogResource(t, f, initial.Session)
	if v := sessionBody(t, current); v.Preparation.State != domain.PreparationUncertain || v.Recovery != domain.NeedsRecovery {
		t.Fatal("session lost uncertainty")
	}
	_, err = sessionClient(f).PrepareSessionWorkspace(ctx, ownerRequest(f.identity, &pb.PrepareSessionWorkspaceRequest{Mutation: acctMutation(current, domain.NewID())}))
	wantAccountCode(t, err, domain.RecoveryRequired)
	archived, err := sessionClient(f).ControlSession(ctx, ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: acctMutation(current, domain.NewID()), Action: pb.SessionAction_SESSION_ACTION_ARCHIVE}))
	if err != nil {
		t.Fatal(err)
	}
	if v := sessionBody(t, archived.Msg.Change.Session); v.Archive != domain.ArchivePending || v.Recovery != domain.NeedsRecovery || v.Dispatch != domain.DispatchPaused {
		t.Fatal("uncertain native work hidden as archived")
	}
}
