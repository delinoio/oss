package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"google.golang.org/protobuf/proto"
)

func TestSessionDeletionRPCRevisionAuthorizationCompletionAndRetry(t *testing.T) {
	f := newAccountFixture(t)
	selection, worker := sessionSelection(t, f)
	creation, target := createSessionFixture(t, f, selection)
	_, other := createSessionFixture(t, f, selection)
	client := sessionClient(f)
	ctx := context.Background()
	req := &pb.DeleteSessionRequest{Mutation: acctMutation(target.Session, domain.NewID())}
	if _, e := client.DeleteSession(ctx, ownerRequest(worker, req)); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("Worker deleted session", e)
	}
	wrong := proto.Clone(req.Mutation).(*pb.Mutation)
	wrong.ExpectedRevision++
	if _, e := client.DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: wrong})); connect.CodeOf(e) != connect.CodeAborted {
		t.Fatal("stale revision accepted", e)
	}
	r, e := client.DeleteSession(ctx, ownerRequest(f.identity, req))
	if e != nil || r.Msg.Job.State != pb.SessionDeletionState_SESSION_DELETION_STATE_PENDING {
		t.Fatal(r, e)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		current, e := client.GetSessionDeletion(ctx, ownerRequest(f.identity, &pb.GetSessionDeletionRequest{SessionId: target.Session.Id}))
		if e != nil {
			t.Fatal(e)
		}
		if current.Msg.Job.State == pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED {
			if current.Msg.Job.ReclaimedBytesKnown {
				t.Fatal("invented reclaimed bytes")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("deletion remained pending", current.Msg.Job)
		}
		time.Sleep(20 * time.Millisecond)
	}
	r, e = client.DeleteSession(ctx, ownerRequest(f.identity, req))
	if e != nil || !r.Msg.Replayed || r.Msg.Job.State != pb.SessionDeletionState_SESSION_DELETION_STATE_SUCCEEDED {
		t.Fatal("exact retry changed deletion", e)
	}
	if _, e := client.CreateSession(ctx, ownerRequest(f.identity, creation)); e == nil {
		t.Fatal("creation receipt resurrected deleted session")
	}
	if _, e := client.GetSessionDeletion(ctx, ownerRequest(worker, &pb.GetSessionDeletionRequest{SessionId: target.Session.Id})); connect.CodeOf(e) != connect.CodePermissionDenied {
		t.Fatal("Worker read owner deletion view", e)
	}
	listed, e := client.ListSessions(ctx, ownerRequest(f.identity, &pb.ListSessionsRequest{IncludeArchived: true}))
	if e != nil || len(listed.Msg.Sessions) != 1 || listed.Msg.Sessions[0].Id != other.Session.Id {
		t.Fatal("other session changed", listed, e)
	}
}

func TestSessionDeletionRPCOriginalWorkerWorkAndIndependentAcknowledgment(t *testing.T) {
	f := newAccountFixture(t)
	selection, identity := sessionSelection(t, f)
	_, target := createSessionFixture(t, f, selection)
	ctx, worker, instance, stream := workspaceStream(t, f, identity, selection.MachineID)
	if !stream.Receive() || stream.Msg().Job == nil {
		t.Fatal("missing workspace assignment", stream.Err())
	}
	current := currentCatalogResource(t, f, target.Session)
	r, e := sessionClient(f).DeleteSession(ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: acctMutation(current, domain.NewID())}))
	if e != nil || r.Msg.Job.WorkersPending != 1 {
		t.Fatal(r, e)
	}
	work, e := worker.ListSessionDeletionWork(ctx, ownerRequest(identity, &pb.ListSessionDeletionWorkRequest{MachineId: string(selection.MachineID), InstanceId: instance}))
	if e != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal(work, e)
	}
	var original domain.SessionDeletionWork
	if e := json.Unmarshal(work.Msg.WorkJson[0], &original); e != nil || original.Validate() != nil {
		t.Fatal(original, e)
	}
	// This report is a controlled Worker fixture. Native removal is independently
	// exercised by the private Worker/real Git deletion fixtures.
	report := &pb.ReportSessionDeletionRequest{RequestId: string(domain.NewID()), MachineId: string(selection.MachineID), InstanceId: instance, SessionId: target.Session.Id, DeletionId: r.Msg.Job.Id, WorkDigest: original.Digest()}
	bad := proto.Clone(report).(*pb.ReportSessionDeletionRequest)
	bad.WorkDigest = "foreign"
	if _, e := worker.ReportSessionDeletion(ctx, ownerRequest(identity, bad)); e == nil {
		t.Fatal("foreign acknowledgement accepted")
	}
	if _, e := worker.ReportSessionDeletion(ctx, ownerRequest(f.identity, report)); e == nil {
		t.Fatal("owner fabricated Worker acknowledgement")
	}
	ack, e := worker.ReportSessionDeletion(ctx, ownerRequest(identity, report))
	if e != nil || ack.Msg.Job.WorkersPending != 0 {
		t.Fatal(ack, e)
	}
	if _, e := worker.ReportSessionDeletion(ctx, ownerRequest(identity, report)); e != nil {
		t.Fatal("original report replay failed", e)
	}
}
