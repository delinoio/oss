// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Publish the fork and claim its first queued input through the public APIs.
// Native identity and checkpoint digests are controlled Worker observations;
// this fixture never launches a native harness or requests inference.
func claimedForkExecutionFixture(t *testing.T) *continuationFixture {
	t.Helper()
	f, child, _ := publishedForkFixture(t)
	f.change.Session = child
	f.enqueue(t, "First independent child input", domain.ExecuteMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	if f.input.Version != 3 || f.input.Fork == nil || f.input.Continuation != nil || f.input.ExecutionID == f.input.Fork.RuntimeID {
		t.Fatal("child lost independent execution and inherited native history")
	}
	f.thread = domain.ID(f.input.Fork.NativeThreadID)
	f.grant(t)
	return f
}

func TestSessionForkExecutionRejectsForeignThreadAndInheritedTurnAtomically(t *testing.T) {
	for _, stage := range []domain.ExecutionEventKind{domain.ExecutionThreadBound, domain.ExecutionInputAccepted} {
		t.Run(string(stage), func(t *testing.T) {
			f := claimedForkExecutionFixture(t)
			ctx := context.Background()
			bad := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Kind: stage, NativeThreadID: string(f.thread)}
			if stage == domain.ExecutionThreadBound {
				bad.Sequence, bad.NativeThreadID = 1, string(domain.NewID())
				bad.Observed = &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}
			} else {
				f.publish(t, domain.ExecutionThreadBound, 1, "")
				bad.Sequence, bad.NativeTurnID = 2, string(f.input.Fork.NativeTurnID)
			}
			child, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || child.Fork == nil {
				t.Fatal("missing published fork metadata", err)
			}
			refs := []struct {
				kind domain.Kind
				id   domain.ID
			}{
				{domain.SessionKind, f.input.SessionID},
				{domain.QueueKind, f.input.InputID},
				{domain.JobKind, domain.ID(f.job.Id)},
				{domain.SessionKind, child.Fork.SourceSessionID},
			}
			before := make([]store.Record, len(refs))
			for i, ref := range refs {
				before[i], err = f.service.Store.Get(ctx, ref.kind, ref.id)
				if err != nil {
					t.Fatal(err)
				}
			}
			raw, _ := json.Marshal(bad)
			_, err = f.workerClient.PublishExecution(ctx, ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
			if domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
				t.Fatal("fork publication accepted foreign or inherited native ownership", err)
			}
			for i, ref := range refs {
				after, err := f.service.Store.Get(ctx, ref.kind, ref.id)
				if err != nil || after.Revision != before[i].Revision || !bytes.Equal(after.Data, before[i].Data) {
					t.Fatal("rejected native identity changed session, input or job ownership", ref.kind, err)
				}
			}
			// Rejection must not poison the original valid thread/new-turn path.
			if stage == domain.ExecutionThreadBound {
				f.publish(t, domain.ExecutionThreadBound, 1, "")
			}
			f.publish(t, domain.ExecutionInputAccepted, 2, "")
			f.finish(t, domain.ExecutionSucceeded)
			completed, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || completed.Execution == nil || completed.Execution.NativeThreadID != string(f.input.Fork.NativeThreadID) || completed.Execution.NativeTurnID != string(f.turn) || completed.PendingInputs != 0 || !completed.Execution.CleanupVerified {
				t.Fatal("valid fork thread/fresh turn failed after rejected publication", err)
			}
		})
	}
}

func TestSessionForkFirstExecutionRecoversLostReportFromForkRuntime(t *testing.T) {
	f := claimedForkExecutionFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	t.Cleanup(cancel)
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	f.publish(t, domain.ExecutionTurnFinished, 3, domain.ExecutionSucceeded)
	// Lose the original final ReportWork, then replace its disconnected Worker.
	// The replacement must recover the original history without resending input.
	if err := f.workerStream.Close(); err != nil {
		t.Fatal(err)
	}
	_, err := f.service.Store.Mutate(ctx, domain.NewID(), "test.fork-worker-lost", nil, func(tx *store.Tx) (any, error) {
		return nil, tx.SetWorkerInstance(domain.ID(f.machine.Id), domain.ID(f.workerInstance), time.Now().UTC().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	f.workerInstance = string(domain.NewID())
	if _, err := f.workerClient.AttachWorker(ctx, ownerRequest(f.workerIdentity, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Version: rpc.Version})); err != nil {
		t.Fatal(err)
	}
	row := f.refresh(t)
	response, err := sessionClient(f.accountFixture).RecoverSessionExecution(ctx, ownerRequest(f.identity, &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, ExpectedExecutionId: string(f.input.ExecutionID)}))
	if err != nil || response.Msg.Change.ExecutionRecoveryJob == nil {
		t.Fatal("completed child could not request lost-report recovery", err)
	}
	var job domain.Job
	var input domain.ExecutionRecoveryRequest
	if domain.Decode(response.Msg.Change.ExecutionRecoveryJob.DocumentJson, &job) != nil || domain.Decode(job.Input, &input) != nil || input.Validate() != nil {
		t.Fatal("invalid child recovery assignment")
	}
	if input.HistoryExecutionID != f.input.Fork.RuntimeID || input.HistoryExecutionID == f.input.ExecutionID || input.JobID != domain.ID(f.job.Id) || input.Completion.ExecutionID != f.input.ExecutionID || input.Completion.NativeThreadID != f.input.Fork.NativeThreadID || input.Completion.NativeTurnID != domain.NativeIdentity(f.turn) {
		t.Fatal("child recovery replaced its original history/report identities")
	}
	stream, err := f.workerClient.WatchWork(ctx, ownerRequest(f.workerIdentity, &pb.WatchWorkRequest{MachineId: f.machine.Id, InstanceId: f.workerInstance}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stream.Close() })
	var claimed *pb.Resource
	for stream.Receive() {
		if stream.Msg().Job != nil {
			claimed = stream.Msg().Job
			break
		}
	}
	if claimed == nil || claimed.Id != response.Msg.Change.ExecutionRecoveryJob.Id {
		t.Fatal("replacement Worker did not claim the recovery job", stream.Err())
	}
	completion := input.Completion
	completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	evidence, _ := json.Marshal(domain.ExecutionRecoveryEvidence{Version: 1, JobID: input.JobID, ReportID: domain.NewID(), Completion: completion})
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(claimed, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: evidence})); err != nil {
		t.Fatal("original child report could not settle through recovery", err)
	}
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.Outcome != domain.ExecutionSucceeded || session.Execution == nil || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || session.PendingInputs != 0 {
		t.Fatal("child recovery lost completion, pause or accepted-input ownership", err)
	}
}
