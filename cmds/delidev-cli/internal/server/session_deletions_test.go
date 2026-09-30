// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
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

func TestSessionDeletionWaitsForBothForwardCleanupReports(t *testing.T) {
	f := newForwardFixture(t)
	_, accepted := f.start(t, 12345, 0, f.identity)
	clientPeer := f.config(t, accepted.Forward, false, f.identity).Peer
	workerPeer := f.config(t, accepted.Forward, true, f.worker).Peer
	for _, peer := range []struct {
		peer     *pb.ForwardPeer
		identity security.Identity
	}{{clientPeer, f.identity}, {workerPeer, f.worker}} {
		if _, err := f.client.ClaimForward(f.ctx, ownerRequest(peer.identity, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: peer.peer})); err != nil {
			t.Fatal(err)
		}
	}
	sessions := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.endpoint)
	request := &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.session.ID), ExpectedRevision: f.session.Revision}}
	if _, err := sessions.DeleteSession(f.ctx, ownerRequest(f.identity, request)); err != nil {
		t.Fatal(err)
	}
	owner := domain.WithPrincipal(f.ctx, domain.Principal{Type: domain.OwnerDevice})
	if _, err := f.service.Store.PurgeDeletedSession(owner, f.session.ID); err == nil {
		t.Fatal("removed original forward evidence before either peer cleaned up")
	}
	value := forwardValue(t, f.observe(t, accepted.Forward.Id, f.identity))
	if value.State != domain.ForwardStopping || value.ClientClean || value.WorkerClean {
		t.Fatal("deletion invented forward cleanup", value)
	}
	if _, err := f.client.ClaimForward(f.ctx, ownerRequest(f.identity, &pb.ClaimForwardRequest{RequestId: string(domain.NewID()), Peer: clientPeer})); err == nil {
		t.Fatal("deletion reopened native forward authority")
	}
	clientReport := &pb.ReportForwardCleanupRequest{RequestId: string(domain.NewID()), Peer: clientPeer}
	if _, err := f.client.ReportForwardCleanup(f.ctx, ownerRequest(f.identity, clientReport)); err != nil {
		t.Fatal("deletion rejected original client cleanup", err)
	}
	if _, err := f.service.Store.PurgeDeletedSession(owner, f.session.ID); err == nil {
		t.Fatal("client cleanup proved Worker cleanup")
	}
	if _, err := f.client.ReportForwardCleanup(f.ctx, ownerRequest(f.identity, clientReport)); err != nil {
		t.Fatal("exact cleanup receipt could not be observed", err)
	}
	if _, err := f.client.ReportForwardCleanup(f.ctx, ownerRequest(f.worker, &pb.ReportForwardCleanupRequest{RequestId: string(domain.NewID()), Peer: workerPeer})); err != nil {
		t.Fatal("deletion rejected original Worker cleanup", err)
	}
	if _, err := f.service.Store.PurgeDeletedSession(owner, f.session.ID); err != nil {
		t.Fatal("confirmed forward cleanup did not release deletion", err)
	}
	if _, err := f.service.Store.Get(owner, domain.ForwardKind, domain.ID(accepted.Forward.Id)); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("deleted forward resource remained", err)
	}
}

func TestSessionDeletionClosesActualForwardSockets(t *testing.T) {
	f := newForwardFixture(t)
	_, accepted := f.start(t, 12345, 0, f.identity)
	endpoint, clientDone, workerDone := f.runPair(t, accepted.Forward, f.identity)
	sessions := delidevv1connect.NewSessionServiceClient(http.DefaultClient, f.endpoint)
	if _, err := sessions.DeleteSession(f.ctx, ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.session.ID), ExpectedRevision: f.session.Revision}})); err != nil {
		t.Fatal(err)
	}
	awaitForward(t, clientDone)
	awaitForward(t, workerDone)
	conn, err := net.DialTimeout("tcp", endpoint, time.Second)
	if err == nil {
		conn.Close()
		t.Fatal("deleted session retained its listener")
	}
	owner := domain.WithPrincipal(f.ctx, domain.Principal{Type: domain.OwnerDevice})
	if _, err := f.service.Store.PurgeDeletedSession(owner, f.session.ID); err != nil {
		t.Fatal("native forward cleanup did not release deletion", err)
	}
}
