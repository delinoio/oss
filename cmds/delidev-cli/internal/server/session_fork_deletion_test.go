// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func publishedForkFixture(t *testing.T) (*continuationFixture, *pb.Resource, domain.ForkJobInput) {
	t.Helper()
	f, _, accepted := acceptedForkFixture(t)
	job, input := forkClaimFixture(t, f, accepted.Job.Id)
	raw, _ := json.Marshal(forkResultFixture(t, input))
	if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	response, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil || response.Msg.Session == nil {
		t.Fatal("child not published", err)
	}
	return f, response.Msg.Session, input
}

func TestSessionForkDeletionRequiresUnpublishedForkToSettle(t *testing.T) {
	f, _, _ := acceptedForkFixture(t)
	source := f.refresh(t)
	_, err := sessionClient(f.accountFixture).DeleteSession(context.Background(), ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(source.ID), ExpectedRevision: source.Revision}}))
	if domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("deletion bypassed unpublished native ownership", err)
	}
	if _, err := f.service.Store.GetSessionDeletion(context.Background(), source.ID); err == nil {
		t.Fatal("irreversible deletion intent created before fork settled")
	}
}

func TestSessionForkDeletionOwnsChildRuntimeBeforeFirstInput(t *testing.T) {
	f, child, input := publishedForkFixture(t)
	_, err := sessionClient(f.accountFixture).DeleteSession(context.Background(), ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: acctMutation(child, domain.NewID())}))
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.workerClient.ListSessionDeletionWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ListSessionDeletionWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal("unstarted child lost cleanup owner", err)
	}
	var w domain.SessionDeletionWork
	if domain.Decode(work.Msg.WorkJson[0], &w) != nil || w.Validate() != nil || w.Fork == nil || w.SessionID != input.ChildSessionID || w.Fork.RuntimeID != input.RuntimeID || len(w.Copies) != 0 || len(w.PreparationDigests) != 1 {
		t.Fatal("invalid child-only cleanup plan")
	}
	if _, err := f.service.Store.Get(context.Background(), domain.SessionKind, input.SourceSessionID); err != nil {
		t.Fatal("child deletion changed parent", err)
	}
}

func TestSessionForkContinuesAfterParentPermanentDeletion(t *testing.T) {
	f, child, input := publishedForkFixture(t)
	source := f.refresh(t)
	deletion, err := sessionClient(f.accountFixture).DeleteSession(context.Background(), ownerRequest(f.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(source.ID), ExpectedRevision: source.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	work, err := f.workerClient.ListSessionDeletionWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ListSessionDeletionWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil || len(work.Msg.WorkJson) != 1 {
		t.Fatal(err)
	}
	var w domain.SessionDeletionWork
	if domain.Decode(work.Msg.WorkJson[0], &w) != nil || w.Validate() != nil || w.Fork != nil {
		t.Fatal("parent adopted the child runtime")
	}
	for _, copy := range w.Copies {
		if copy.ExecutionID == input.RuntimeID {
			t.Fatal("parent deletion owns child history")
		}
	}
	// Controlled Worker acknowledgement tests the server transaction. Independent
	// real-filesystem tests prove runtime/worktree removal and source preservation.
	if _, err := f.workerClient.ReportSessionDeletion(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportSessionDeletionRequest{RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, SessionId: string(source.ID), DeletionId: deletion.Msg.Job.Id, WorkDigest: w.Digest()})); err != nil {
		t.Fatal(err)
	}
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	if _, err := f.service.Store.PurgeDeletedSession(owner, source.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Store.Get(owner, domain.JobKind, w.Copies[len(w.Copies)-1].JobID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("parent job survived purge", err)
	}
	raw, _ := json.Marshal(domain.SessionInput{Prompt: "Continue after parent deletion", Mode: domain.ExecuteMode})
	if _, err := sessionClient(f.accountFixture).EnqueueInput(context.Background(), ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: child.Id, DocumentJson: raw})); err != nil {
		t.Fatal(err)
	}
	current, err := f.service.Store.Get(owner, domain.SessionKind, domain.ID(child.Id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: child.Id, ExpectedRevision: current.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME})); err != nil {
		t.Fatal("child depended on purged parent", err)
	}
	f.change.Session = child
	f.claim(t)
	if f.input.Startup == nil || input.Startup == nil || *f.input.Startup != *input.Startup {
		t.Fatal("parent deletion replaced the child-owned executable selection")
	}
}

func TestSessionForkResponsesRetainCorrelation(t *testing.T) {
	f, request, accepted := acceptedForkFixture(t)
	correlation := string(domain.NewID())
	retry := ownerRequest(f.identity, request)
	retry.Header().Set(rpc.CorrelationHeader, correlation)
	owner := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.OwnerDevice})
	response, err := f.service.ForkSession(owner, retry)
	if err != nil || response.Header().Get(rpc.CorrelationHeader) != correlation {
		t.Fatal("fork acceptance lost correlation", err)
	}
	status := ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: accepted.Job.Id})
	status.Header().Set(rpc.CorrelationHeader, correlation)
	observed, err := f.service.GetSessionFork(owner, status)
	if err != nil || observed.Header().Get(rpc.CorrelationHeader) != correlation {
		t.Fatal("fork observation lost correlation", err)
	}
}
