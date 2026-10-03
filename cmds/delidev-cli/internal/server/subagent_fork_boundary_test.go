// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func TestSubagentClosedTreeCannotAcquireForkCheckpoint(t *testing.T) {
	for _, version := range []uint32{1, 2} {
		t.Run(map[uint32]string{1: "paused-completion", 2: "unproved-checkpoint"}[version], func(t *testing.T) {
			f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixture(t), thread: domain.NewID()}
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			f.publish(t, domain.ExecutionThreadBound, 1, "")
			f.publish(t, domain.ExecutionInputAccepted, 2, "")
			event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 3, Kind: domain.ExecutionSubagentObserved, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), Subagents: []domain.SubagentObservation{{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentCompleted, Source: domain.CodexCollaborationSource, SourceID: "original-closed-spawn"}}}
			raw, _ := json.Marshal(event)
			if _, err := f.workerClient.PublishExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw})); err != nil {
				t.Fatal(err)
			}
			f.publish(t, domain.ExecutionTurnFinished, 4, domain.ExecutionSucceeded)
			completion := domain.ExecutionCompletion{Version: version, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 4, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
			if version == 2 {
				completion.NativeCheckpointDigest = strings.Repeat("ab", 32)
			}
			if err := completion.Validate(); err != nil {
				t.Fatal(err)
			}
			raw, _ = json.Marshal(completion)
			if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw})); err != nil {
				t.Fatal(err)
			}
			before := f.refresh(t)
			session, err := store.Decode[domain.Session](before)
			if err != nil || session.Execution == nil || len(session.Execution.Subagents) != 1 || !session.Execution.Subagents.Closed() || session.Execution.CleanupVerified != (version == 1) || session.Dispatch != domain.DispatchPaused || version == 2 && session.Recovery != domain.NeedsRecovery {
				t.Fatal("closed child acquired unproved completion authority", err)
			}
			request := &pb.ForkSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(before.ID), ExpectedRevision: before.Revision}, ExpectedTurnId: string(f.turn), Name: "Unsupported observed-child fork"}
			if _, err := sessionClient(f.accountFixture).ForkSession(context.Background(), ownerRequest(f.identity, request)); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
				t.Fatal("observed tree acquired fork authority", err)
			}
			after := f.refresh(t)
			if before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
				t.Fatal("rejected fork changed original session")
			}
		})
	}
}
