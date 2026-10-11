// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func nativeShellServerFixture(t *testing.T) (*continuationFixture, store.Record, domain.Session, *pb.RunNativeShellRequest, *pb.Resource) {
	t.Helper()
	f := newContinuationFixtureProfile(t, domain.ExecutionSucceeded, domain.Codex)
	f.workerStream.Close()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.native-shell-capability", nil, func(tx *store.Tx) (any, error) {
		r, m, e := activeMachine(tx, f.selection.MachineID)
		if e != nil {
			return nil, e
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.CodexNativeShellV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	before := f.refresh(t)
	prior, err := store.Decode[domain.Session](before)
	if err != nil {
		t.Fatal(err)
	}
	req := &pb.RunNativeShellRequest{Mutation: acctMutation(resourceForTest(before), domain.NewID()), Command: "  printf 'explicit native command'  ", Access: pb.NativeShellAccess_NATIVE_SHELL_ACCESS_FULL_ACCESS}
	accepted, err := sessionClient(f.accountFixture).RunNativeShell(context.Background(), ownerRequest(f.identity, req))
	if err != nil {
		t.Fatal(err)
	}
	return f, before, prior, req, accepted.Msg.Action
}
func TestNativeShellHumanReceiptExactReplayAndQueuedCancellation(t *testing.T) {
	f, _, prior, req, resource := nativeShellServerFixture(t)
	client := sessionClient(f.accountFixture)
	ctx := context.Background()
	replay, err := client.RunNativeShell(ctx, ownerRequest(f.identity, req))
	if err != nil || !replay.Msg.Replayed || replay.Msg.Action.Id != resource.Id {
		t.Fatal("acceptance replay created another native action", err)
	}
	var action domain.NativeShellAction
	if domain.Decode(resource.DocumentJson, &action) != nil || action.Validate() != nil || action.Command.Command != req.Command || action.Command.Actor.Type != domain.OwnerDevice || action.Command.Actor.DeviceID != "" {
		t.Fatal("receipt replaced original human command identity")
	}
	changed := *req
	changed.Command = "another command"
	if _, err := client.RunNativeShell(ctx, ownerRequest(f.identity, &changed)); err == nil {
		t.Fatal("receipt reused for changed command")
	}
	if _, err := client.RunNativeShell(ctx, ownerRequest(f.workerIdentity, req)); err == nil {
		t.Fatal("Worker authenticated agent flow authorized native shell")
	}
	contextReply, contextErr := client.GetSessionContext(ctx, ownerRequest(f.identity, &pb.GetSessionContextRequest{SessionId: string(action.SessionID)}))
	var contextView sessionContextView
	if contextErr != nil || domain.Decode(contextReply.Msg.DocumentJson, &contextView) != nil || contextView.ManualAction != nil || contextView.AutomaticBoundary != nil {
		t.Fatal("native shell fabricated a compaction/context observation", contextErr)
	}
	pending, _ := store.Decode[domain.Session](f.refresh(t))
	if !reflect.DeepEqual(prior.Execution, pending.Execution) || !reflect.DeepEqual(prior.Compaction, pending.Compaction) || pending.ContextRevision != prior.ContextRevision || pending.LastCompactionJobID != prior.LastCompactionJobID {
		t.Fatal("acceptance changed ordinary execution/context")
	}
	canceled, err := client.CancelNativeShell(ctx, ownerRequest(f.identity, &pb.CancelNativeShellRequest{Mutation: acctMutation(resource, domain.NewID()), SessionId: string(action.SessionID)}))
	if err != nil {
		t.Fatal(err)
	}
	var done domain.NativeShellAction
	domain.Decode(canceled.Msg.Action.DocumentJson, &done)
	after, _ := store.Decode[domain.Session](f.refresh(t))
	if done.State != domain.NativeShellCanceled || done.Claim != nil || done.Observation != nil || after.CompactionJobID != "" || !reflect.DeepEqual(prior.Execution, after.Execution) || after.ContextRevision != prior.ContextRevision {
		t.Fatal("queued cancel invented native execution or rewrote checkpoint")
	}
}
func TestNativeShellClaimPublicationAndTypedCompletionPreserveCheckpoint(t *testing.T) {
	f, _, prior, _, resource := nativeShellServerFixture(t)
	ctx := context.Background()
	var action domain.NativeShellAction
	domain.Decode(resource.DocumentJson, &action)
	jobRecord, err := f.service.Store.Get(ctx, domain.JobKind, action.JobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](jobRecord)
	if err != nil {
		t.Fatal(err)
	}
	var input domain.SessionCompactionInput
	if domain.DecodeCompactionInput(job.Input, &input) != nil || input.Validate() != nil || input.Version != 5 || input.NativeShell == nil || input.Revert != nil {
		t.Fatal("shell assignment entered compaction variant")
	}
	_, err = f.service.Store.Mutate(ctx, domain.NewID(), "fixture.native-shell-work", nil, func(tx *store.Tx) (any, error) {
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(f.workerInstance), f.workerDevice
		return tx.PutJob(jobRecord.ID, jobRecord.Revision, jobRecord.SessionID, jobRecord.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
	jobRecord, err = f.service.Store.Get(ctx, domain.JobKind, action.JobID)
	if err != nil {
		t.Fatal(err)
	}
	claim := &pb.ClaimNativeShellRequest{Mutation: acctMutation(resource, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance}
	claimed, err := f.workerClient.ClaimNativeShell(ctx, ownerRequest(f.workerIdentity, claim))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := f.workerClient.ClaimNativeShell(ctx, ownerRequest(f.workerIdentity, claim))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("claim replay lost original reservation", err)
	}
	if _, err := f.workerClient.ClaimNativeShell(ctx, ownerRequest(f.workerIdentity, &pb.ClaimNativeShellRequest{Mutation: acctMutation(claimed.Msg.Action, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance})); err == nil {
		t.Fatal("second claim acquired send authority")
	}
	domain.Decode(claimed.Msg.Action.DocumentJson, &action)
	observation := *action.Observation
	observation.Sequence++
	observation.State = domain.NativeShellRunning
	observation.Acknowledged = true
	observation.NativeTurnID = domain.NativeIdentity(domain.NewID())
	observation.NativeItemID = "original-item"
	raw, _ := json.Marshal(observation)
	running, err := f.workerClient.PublishNativeShell(ctx, ownerRequest(f.workerIdentity, &pb.PublishNativeShellRequest{Mutation: acctMutation(claimed.Msg.Action, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, ObservationJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	exit, duration, aggregate := int32(17), int64(8), "native aggregate"
	observation.Sequence++
	observation.State = domain.NativeShellExited
	observation.Output = "partial output"
	observation.ExitCode = &exit
	observation.DurationMS = &duration
	observation.AggregatedOutput = &aggregate
	raw, _ = json.Marshal(observation)
	_, err = f.workerClient.PublishNativeShell(ctx, ownerRequest(f.workerIdentity, &pb.PublishNativeShellRequest{Mutation: acctMutation(running.Msg.Action, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, ObservationJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	observation.Sequence++
	observation.CleanupVerified = true
	ref := &domain.NativeShellRef{JobID: action.JobID, ActionID: action.ActionID, ExecutionID: action.ExecutionID, CheckpointDigest: strings.Repeat("ab", 32), NativeDigest: strings.Repeat("cd", 32)}
	raw, _ = json.Marshal(domain.NativeShellResult{Version: 1, Observation: observation, History: ref})
	_, err = f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(resourceForTest(jobRecord), domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
	if err != nil {
		t.Fatal("typed shell report was dispatched as compaction", err)
	}
	after, _ := store.Decode[domain.Session](f.refresh(t))
	if after.CompactionJobID != "" || !reflect.DeepEqual(after.NativeShell, ref) || !reflect.DeepEqual(after.Compaction, prior.Compaction) || !reflect.DeepEqual(after.Execution, prior.Execution) || after.ContextRevision != prior.ContextRevision || after.Outcome != prior.Outcome || after.LastCompactionJobID != prior.LastCompactionJobID {
		t.Fatal("shell report rewrote ordinary checkpoint, outcome or context")
	}
}
