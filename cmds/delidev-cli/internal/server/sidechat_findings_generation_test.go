// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// putSidechatAnswer stores one complete assistant reply owned by the Sidechat
// child for the given answer execution.
func putSidechatAnswer(t *testing.T, f *continuationFixture, execution domain.ID, body string) store.Record {
	t.Helper()
	id := domain.NewID()
	result, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "test.findings-generation", id, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.MessageKind, id, 0, domain.ID(f.change.Session.Id), "", domain.ExecutionMessage{ExecutionID: execution, NativeThreadID: string(domain.NewID()), NativeTurnID: string(domain.NewID()), NativeID: "fixture", Role: domain.AssistantMessage, State: domain.MessageComplete, Text: body, FirstSequence: 1, LastSequence: 2})
	})
	if err != nil {
		t.Fatal(err)
	}
	var r store.Record
	if domain.Decode(result.Data, &r) != nil {
		t.Fatal("message fixture")
	}
	return r
}

func sidechatFindingsRequest(t *testing.T, parent, child *continuationFixture, rows ...store.Record) *pb.SendSidechatFindingsRequest {
	t.Helper()
	selections := make([]*pb.SidechatFindingSelection, 0, len(rows))
	for _, row := range rows {
		selections = append(selections, &pb.SidechatFindingSelection{MessageId: string(row.ID), ExpectedRevision: row.Revision})
	}
	c, p := child.refresh(t), parent.refresh(t)
	return &pb.SendSidechatFindingsRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(c.ID), ExpectedRevision: c.Revision}, ParentId: string(p.ID), ExpectedParentRevision: p.Revision, Messages: selections}
}

func parentPendingInputs(t *testing.T, parent *continuationFixture) (uint64, uint32) {
	t.Helper()
	record := parent.refresh(t)
	var session domain.Session
	if domain.Decode(record.Data, &session) != nil {
		t.Fatal("parent session")
	}
	return record.Revision, session.PendingInputs
}

func TestSidechatFindingsFenceSupersededAnswerAfterSuccessfulRetry(t *testing.T) {
	parent, child := completedRetrySidechatFixture(t)
	ctx := context.Background()
	original := child.input.ExecutionID
	oldReply := putSidechatAnswer(t, child, original, "Superseded reply")
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(ctx, ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	finishRetryForkFixture(t, child, request)
	retried := child.input.ExecutionID
	newReply := putSidechatAnswer(t, child, retried, "Current reply")
	completeReadOnlySidechat(t, child, domain.ExecutionSucceeded)
	if view := retryViewFixture(t, child, request.Mutation.RequestId); view.CurrentAnswer != retried || retried == original {
		t.Fatal("retry did not select the new answer generation")
	}
	revision, pending := parentPendingInputs(t, parent)

	// Direct RPC selection of the superseded answer rejects atomically, even
	// when combined with a valid current reply, and leaves the parent untouched.
	for _, rows := range [][]store.Record{{oldReply}, {newReply, oldReply}} {
		if _, err := sessionClient(child.accountFixture).SendSidechatFindings(ctx, ownerRequest(child.identity, sidechatFindingsRequest(t, parent, child, rows...))); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
			t.Fatal("superseded answer accepted", err)
		}
		if after, queued := parentPendingInputs(t, parent); after != revision || queued != pending {
			t.Fatal("rejected findings changed the parent")
		}
	}

	// The current answer queues once and replays the exact receipt.
	valid := sidechatFindingsRequest(t, parent, child, newReply)
	result, err := sessionClient(child.accountFixture).SendSidechatFindings(ctx, ownerRequest(child.identity, valid))
	if err != nil {
		t.Fatal(err)
	}
	var queue domain.QueuedInput
	if domain.Decode(result.Msg.Change.Input.DocumentJson, &queue) != nil || queue.Prompt != "Selected Sidechat findings:\n\nCurrent reply" {
		t.Fatal("current findings changed")
	}
	replay, err := sessionClient(child.accountFixture).SendSidechatFindings(ctx, ownerRequest(child.identity, proto.Clone(valid).(*pb.SendSidechatFindingsRequest)))
	if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.Input.Id != result.Msg.Change.Input.Id {
		t.Fatal("findings receipt replay", err)
	}
	if _, queued := parentPendingInputs(t, parent); queued != pending+1 {
		t.Fatal("current findings were not queued exactly once")
	}
}

func TestSidechatFindingsFailedRetryKeepsPreviousAnswerTransferable(t *testing.T) {
	parent, child := completedRetrySidechatFixture(t)
	ctx := context.Background()
	original := child.input.ExecutionID
	oldReply := putSidechatAnswer(t, child, original, "Selected reply")
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(ctx, ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	finishRetryForkFixture(t, child, request)
	failedReply := putSidechatAnswer(t, child, child.input.ExecutionID, "Failed attempt reply")
	completeReadOnlySidechat(t, child, domain.ExecutionFailed)
	if view := retryViewFixture(t, child, request.Mutation.RequestId); view.CurrentAnswer != original {
		t.Fatal("failed retry replaced the selected answer")
	}
	_, pending := parentPendingInputs(t, parent)
	if _, err := sessionClient(child.accountFixture).SendSidechatFindings(ctx, ownerRequest(child.identity, sidechatFindingsRequest(t, parent, child, failedReply))); domain.SafeError(rpc.ClientError(err)).Code != domain.Conflict {
		t.Fatal("unselected failed generation accepted", err)
	}
	result, err := sessionClient(child.accountFixture).SendSidechatFindings(ctx, ownerRequest(child.identity, sidechatFindingsRequest(t, parent, child, oldReply)))
	if err != nil {
		t.Fatal(err)
	}
	var queue domain.QueuedInput
	if domain.Decode(result.Msg.Change.Input.DocumentJson, &queue) != nil || queue.Prompt != "Selected Sidechat findings:\n\nSelected reply" {
		t.Fatal("selected answer was not transferable")
	}
	if _, queued := parentPendingInputs(t, parent); queued != pending+1 {
		t.Fatal("selected findings were not queued exactly once")
	}
}
