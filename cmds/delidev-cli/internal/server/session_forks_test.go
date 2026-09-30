// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/workspace"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func acceptedForkFixture(t *testing.T) (*continuationFixture, *pb.ForkSessionRequest, *pb.ForkSessionResponse) {
	t.Helper()
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	f.enqueue(t, "Keep this input in the source only", domain.ExecuteMode)
	source := f.refresh(t)
	request := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(source.ID), ExpectedRevision: source.Revision}, ExpectedTurnId: string(f.turn), Name: "Independent child"}
	response, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	return f, request, response.Msg
}

func forkClaimFixture(t *testing.T, f *continuationFixture, id string) (*pb.Resource, domain.ForkJobInput) {
	t.Helper()
	for {
		if !f.workerStream.Receive() {
			t.Fatal(f.workerStream.Err())
		}
		job := f.workerStream.Msg().Job
		if job == nil {
			continue
		}
		if job.Id != id {
			t.Fatal("wrong job claimed")
		}
		var envelope domain.Job
		var input domain.ForkJobInput
		if domain.Decode(job.DocumentJson, &envelope) != nil || envelope.Type != domain.ForkSessionJob || domain.Decode(envelope.Input, &input) != nil || input.Validate() != nil {
			t.Fatal("invalid fork assignment")
		}
		return job, input
	}
}

func forkResultFixture(t *testing.T, input domain.ForkJobInput) domain.ForkJobResult {
	t.Helper()
	var source workspace.Manifest
	if domain.Decode(input.SourceAssignment.Manifest, &source) != nil {
		t.Fatal("source manifest")
	}
	preparation := workspace.PrepareRequest{SessionID: input.ChildSessionID, MachineID: input.SourceAssignment.MachineID, Type: domain.GeneralChat, ForkSourceID: input.SourceSessionID, ForkSourcePath: source.PrimaryPath, Repositories: []workspace.RepositorySpec{}}
	raw, _ := json.Marshal(preparation)
	digest := sha256.Sum256(raw)
	manifest := workspace.Manifest{Version: 1, SessionID: input.ChildSessionID, MachineID: input.SourceAssignment.MachineID, Type: domain.GeneralChat, State: workspace.Ready, InputDigest: hex.EncodeToString(digest[:]), PrimaryPath: filepath.Join(filepath.Dir(filepath.Dir(source.PrimaryPath)), string(input.ChildSessionID), "chat"), Repositories: []workspace.PreparedRepository{}, CreatedAt: time.Now().UTC()}
	manifestRaw, _ := json.Marshal(manifest)
	return domain.ForkJobResult{Version: 1, ChildSessionID: input.ChildSessionID, RuntimeID: input.RuntimeID, NativeThreadID: domain.NativeIdentity(domain.NewID()), NativeTurnID: input.Completion.NativeTurnID, CheckpointDigest: strings.Repeat("ab", 32), Preparation: raw, Manifest: manifestRaw, CleanupVerified: true}
}

func TestSessionForkPublishesOneIndependentChildAndDoesNotCopyQueuedInput(t *testing.T) {
	f, request, accepted := acceptedForkFixture(t)
	before := f.refresh(t)
	if accepted.Session != nil {
		t.Fatal("child published before native/workspace completion")
	}
	replay, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Job.Id != accepted.Job.Id {
		t.Fatal("acceptance replay created another child", err)
	}
	if err := f.service.dispatchExecution(context.Background(), before); domain.SafeError(err).Code != domain.Conflict {
		t.Fatal("source continuation bypassed fork ownership", err)
	}
	job, input := forkClaimFixture(t, f, accepted.Job.Id)
	result := forkResultFixture(t, input)
	raw, _ := json.Marshal(result)
	report := &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}
	if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, report)); err != nil {
		t.Fatal(err)
	}
	final, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: accepted.Job.Id}))
	if err != nil || final.Msg.Session == nil {
		t.Fatal("complete child absent", err)
	}
	after := f.refresh(t)
	if !bytes.Equal(before.Data, after.Data) || before.Revision != after.Revision {
		t.Fatal("fork modified source session")
	}
	var child domain.Session
	if domain.Decode(final.Msg.Session.DocumentJson, &child) != nil || child.Fork == nil || child.PendingInputs != 0 || child.LastInputSequence != 0 || child.InitialExecution != nil || child.CurrentExecution != nil || child.Dispatch != domain.DispatchPaused || child.Fork.SourceSessionID != before.ID || child.Fork.SourceTurnID != domain.NativeIdentity(f.turn) {
		t.Fatal("invalid child boundary/empty state")
	}
	replay, err = sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || replay.Msg.Session == nil || replay.Msg.Session.Id != final.Msg.Session.Id {
		t.Fatal("successful receipt lost child", err)
	}
	// Current Agent edits cannot replace inherited settings. The child owns a
	// new queue item and execution, with the exact original account/configuration.
	f.mutateAgent(t, func(agent *domain.Agent) { agent.Effort = "low" })
	queued, _ := json.Marshal(domain.SessionInput{Prompt: "Continue the child only", Mode: domain.ExecuteMode})
	if _, err := sessionClient(f.accountFixture).EnqueueInput(context.Background(), ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: final.Msg.Session.Id, DocumentJson: queued})); err != nil {
		t.Fatal(err)
	}
	childRecord, err := f.service.Store.Get(context.Background(), domain.SessionKind, domain.ID(final.Msg.Session.Id))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(childRecord.ID), ExpectedRevision: childRecord.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME})); err != nil {
		t.Fatal("child resume", err)
	}
	childRecord, err = f.service.Store.Get(context.Background(), domain.SessionKind, childRecord.ID)
	if err != nil {
		t.Fatal(err)
	}
	var executing domain.Session
	if domain.Decode(childRecord.Data, &executing) != nil || executing.CurrentExecution == nil {
		t.Fatal("child execution absent")
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		row, err := tx.SessionExecutionJob(childRecord.ID, executing.CurrentExecution.ID)
		if err != nil {
			return err
		}
		job, err := store.Decode[domain.Job](row)
		if err != nil {
			return err
		}
		var assignment domain.ExecutionJobInput
		if domain.Decode(job.Input, &assignment) != nil || assignment.Validate() != nil || assignment.Fork == nil || assignment.Continuation != nil || assignment.ConfigurationDigest != input.SourceAssignment.ConfigurationDigest || assignment.AccountID != input.SourceAssignment.AccountID || assignment.Input.Prompt != "Continue the child only" || !executing.OwnsExecution(assignment) {
			t.Fatal("fork continuation rerouted/replayed source")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSessionForkRejectsActiveSourceAndInvalidCompletion(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	row := f.refresh(t)
	var original domain.Session
	if domain.Decode(row.Data, &original) != nil {
		t.Fatal("invalid source")
	}
	setSource := func(value domain.Session) {
		t.Helper()
		_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.fork-source", value, func(tx *store.Tx) (any, error) {
			current, err := tx.Get(domain.SessionKind, row.ID)
			if err != nil {
				return nil, err
			}
			return tx.Put(domain.SessionKind, row.ID, current.Revision, row.SessionID, row.ProjectID, value)
		})
		if err != nil {
			t.Fatal(err)
		}
		row = f.refresh(t)
	}
	active := original
	active.ActiveExecutionID = domain.NewID()
	setSource(active)
	activeRequest := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, ExpectedTurnId: string(f.turn), Name: "Active source"}
	if _, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, activeRequest)); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("active source accepted", err)
	}
	setSource(original)
	request := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(row.ID), ExpectedRevision: row.Revision}, ExpectedTurnId: string(domain.NewID()), Name: "Wrong boundary"}
	if _, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request)); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("wrong boundary accepted", err)
	}
	request.ExpectedTurnId = string(f.turn)
	accepted, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	job, input := forkClaimFixture(t, f, accepted.Msg.Job.Id)
	result := forkResultFixture(t, input)
	result.CleanupVerified = false
	raw, _ := json.Marshal(result)
	if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
	observed, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil || observed.Msg.Session != nil {
		t.Fatal("partial child published", err)
	}
	var state domain.Job
	if domain.Decode(observed.Msg.Job.DocumentJson, &state) != nil || state.State != domain.JobUncertain {
		t.Fatal("unknown cleanup lost uncertainty")
	}
}

func TestSessionForkFailedCopyReleasesSourceWithoutPublishingChild(t *testing.T) {
	f, request, accepted := acceptedForkFixture(t)
	before := f.refresh(t)
	job, _ := forkClaimFixture(t, f, accepted.Job.Id)
	_, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, Problem: &pb.ErrorDetail{Code: string(domain.Conflict)}}))
	if err != nil {
		t.Fatal(err)
	}
	observed, err := sessionClient(f.accountFixture).GetSessionFork(context.Background(), ownerRequest(f.identity, &pb.GetSessionForkRequest{JobId: job.Id}))
	if err != nil || observed.Msg.Session != nil {
		t.Fatal("failed copy published a child", err)
	}
	var state domain.Job
	if domain.Decode(observed.Msg.Job.DocumentJson, &state) != nil || state.State != domain.JobFailed || state.Problem == nil || state.Problem.Code != domain.Conflict {
		t.Fatal("definite copy failure retained an uncertain job")
	}
	after := f.refresh(t)
	if !bytes.Equal(before.Data, after.Data) || before.Revision != after.Revision {
		t.Fatal("failed fork changed source session")
	}
	request.Mutation.RequestId = string(domain.NewID())
	retry, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request))
	if err != nil || retry.Msg.Job.Id == job.Id {
		t.Fatal("verified failed copy retained the source reservation", err)
	}
}
