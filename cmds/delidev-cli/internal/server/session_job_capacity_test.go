// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func sessionJobCountFixture(t *testing.T, db *store.Store, session domain.ID) int {
	t.Helper()
	count, after := 0, domain.ID("")
	for {
		rows, err := db.List(context.Background(), store.Filter{Kind: domain.JobKind, SessionID: session, After: after, Limit: 200})
		if err != nil {
			t.Fatal(err)
		}
		count += len(rows)
		if len(rows) < 200 {
			return count
		}
		after = rows[len(rows)-1].ID
	}
}

// Seed exact retained history, not N additional jobs atop fixture-owned work.
// Canceled, unclaimed metadata jobs exercise the count bound without claiming
// native/account authority or running any harness.
func fillSessionJobsFixture(t *testing.T, db *store.Store, session, machine domain.ID, target int) {
	t.Helper()
	count := sessionJobCountFixture(t, db, session)
	if count > target {
		t.Fatal("fixture already exceeds target", count, target)
	}
	_, err := db.Mutate(context.Background(), domain.NewID(), "fixture.exact-job-capacity", target, func(tx *store.Tx) (any, error) {
		for n := count; n < target; n++ {
			if _, err := tx.PutJob(domain.NewID(), 0, session, "", domain.Job{Type: domain.GenerateSessionTitleJob, State: domain.JobCanceled, MachineID: machine, Input: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
				return nil, err
			}
		}
		return nil, nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSidechatRetryLastCapacityRecoversAndPlansPermanentCleanup(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	ctx := context.Background()
	sessionID := domain.ID(child.change.Session.Id)
	fillSessionJobsFixture(t, child.service.Store, sessionID, domain.ID(child.machine.Id), 4093)
	request := retryRequestFixture(t, child)
	for range 2 {
		if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(ctx, ownerRequest(child.identity, request)); err != nil {
			t.Fatal(err)
		}
	}
	if count := sessionJobCountFixture(t, child.service.Store, sessionID); count != 4094 {
		t.Fatal("retry receipt consumed capacity twice", count)
	}
	finishRetryForkFixture(t, child, request)
	event := domain.ExecutionEvent{Version: 1, ExecutionID: child.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(child.thread), Observed: &domain.ObservedExecutionSettings{Model: child.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}}
	raw, _ := json.Marshal(event)
	if _, err := child.workerClient.PublishExecution(ctx, ownerRequest(child.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(child.job, domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, EventJson: raw})); err != nil {
		t.Fatal(err)
	}
	child.publish(t, domain.ExecutionInputAccepted, 2, "")
	child.publish(t, domain.ExecutionTurnFinished, 3, domain.ExecutionSucceeded)
	// Lose final ReportWork. Only original retained history may settle recovery.
	if err := child.workerStream.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := child.service.Store.Mutate(ctx, domain.NewID(), "fixture.capacity-worker-lost", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(domain.ID(child.machine.Id), domain.ID(child.workerInstance), time.Now().UTC().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	child.workerInstance = string(domain.NewID())
	if _, err = child.workerClient.AttachWorker(ctx, ownerRequest(child.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	row := child.refresh(t)
	recoverRequest := &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, ExpectedExecutionId: string(child.input.ExecutionID)}
	response, err := sessionClient(child.accountFixture).RecoverSessionExecution(ctx, ownerRequest(child.identity, recoverRequest))
	if err != nil || response.Msg.Change.ExecutionRecoveryJob == nil {
		t.Fatal("reserved recovery was unavailable", err)
	}
	if _, err = sessionClient(child.accountFixture).RecoverSessionExecution(ctx, ownerRequest(child.identity, recoverRequest)); err != nil {
		t.Fatal("recovery receipt replay lost capacity", err)
	}
	row = child.refresh(t)
	duplicate := &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, ExpectedExecutionId: string(child.input.ExecutionID)}
	observed, err := sessionClient(child.accountFixture).RecoverSessionExecution(ctx, ownerRequest(child.identity, duplicate))
	if err != nil || observed.Msg.Change.ExecutionRecoveryJob.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
		t.Fatal("queued recovery consumed another slot", err)
	}
	if count := sessionJobCountFixture(t, child.service.Store, sessionID); count != 4096 {
		t.Fatal("recovery escaped reserved capacity", count)
	}
	pf := &publicationFixture{authorityFixture: &authorityFixture{service: child.service, client: child.workerClient, workerToken: child.workerIdentity.Token, device: child.workerDevice, instance: domain.ID(child.workerInstance), input: child.input, job: domain.ID(child.job.Id)}}
	completeRecovery(t, pf, response.Msg.Change.ExecutionRecoveryJob)
	row = child.refresh(t)
	if _, err = sessionClient(child.accountFixture).DeleteSession(ctx, ownerRequest(child.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}})); err != nil {
		t.Fatal("admitted history cannot enter permanent cleanup", err)
	}
	deletion, err := child.service.Store.GetSessionDeletion(ctx, sessionID)
	if err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(deletion)
	if err != nil || len(raw) > domain.MaxSessionDeletionBytes {
		t.Fatal("cleanup envelope overflow", err)
	}
	if count := sessionJobCountFixture(t, child.service.Store, sessionID); count > 4096 {
		t.Fatal("cleanup grew retained history", count)
	}
}

func TestExecutionRecoveryFullHistoryRejectsWithoutJob(t *testing.T) {
	f := recoveryFixture(t, domain.ExecutionSucceeded)
	fillSessionJobsFixture(t, f.service.Store, f.input.SessionID, f.input.MachineID, 4096)
	request := recoveryRequest(t, f)
	client := sessionClient(&accountFixture{endpoint: Endpoint{URL: f.http.URL}, identity: f.service.Identity})
	_, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, request))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("full history created recovery", err)
	}
	if count := sessionJobCountFixture(t, f.service.Store, f.input.SessionID); count != 4096 {
		t.Fatal("rejected recovery changed jobs", count)
	}
}
