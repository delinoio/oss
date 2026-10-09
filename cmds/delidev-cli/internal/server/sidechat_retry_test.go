// SPDX-License-Identifier: Apache-2.0
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"google.golang.org/protobuf/proto"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func completedRetrySidechatFixture(t *testing.T) (*continuationFixture, *continuationFixture) {
	t.Helper()
	parent, child := publishedSidechatFixture(t)
	_, err := parent.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-capability", nil, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.MachineKind, domain.ID(parent.machine.Id))
		if err != nil {
			return nil, err
		}
		m, err := store.Decode[domain.Machine](r)
		if err != nil {
			return nil, err
		}
		m.WorkerCapabilities = append(m.WorkerCapabilities, domain.SidechatQuestionRetryV1)
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	base := *parent.firstDispatchFixture
	base.change = &pb.SessionChange{Session: child}
	f := &continuationFixture{firstDispatchFixture: &base}
	f.enqueue(t, "What is the current state?", domain.ExecuteMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	f.thread = domain.ID(f.input.Fork.NativeThreadID)
	completeReadOnlySidechat(t, f, domain.ExecutionSucceeded)
	return parent, f
}
func completeReadOnlySidechat(t *testing.T, f *continuationFixture, outcome domain.ExecutionOutcome) {
	t.Helper()
	f.grant(t)
	event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(f.thread), Observed: &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}}
	raw, _ := json.Marshal(event)
	_, err := f.workerClient.PublishExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	f.finish(t, outcome)
}
func retryViewFixture(t *testing.T, f *continuationFixture, generation string) sidechatRetryView {
	t.Helper()
	response, err := sessionClient(f.accountFixture).GetSidechatQuestionRetry(context.Background(), ownerRequest(f.identity, &pb.GetSidechatQuestionRetryRequest{SessionId: f.change.Session.Id, RequestId: generation}))
	if err != nil {
		t.Fatal(err)
	}
	var view sidechatRetryView
	if domain.Decode(response.Msg.DocumentJson, &view) != nil {
		t.Fatal("invalid retry view")
	}
	return view
}
func retryRequestFixture(t *testing.T, f *continuationFixture) *pb.RetrySidechatQuestionRequest {
	t.Helper()
	view := retryViewFixture(t, f, "")
	if !view.Eligible {
		t.Fatalf("not eligible: %+v", view.Problem)
	}
	return &pb.RetrySidechatQuestionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: f.change.Session.Id, ExpectedRevision: view.ChildRevision}, QuestionId: string(view.QuestionID), ExpectedQuestionRevision: view.QuestionRevision, ExpectedParentRevision: view.ParentRevision, ExpectedParentTurnId: string(view.ParentTurnID)}
}
func finishRetryForkFixture(t *testing.T, f *continuationFixture, request *pb.RetrySidechatQuestionRequest) domain.ForkJobInput {
	t.Helper()
	view := retryViewFixture(t, f, request.Mutation.RequestId)
	g := view.Generations[len(view.Generations)-1]
	job, input := forkClaimFixture(t, f, string(g.ForkJobID))
	if input.Retry == nil || input.ChildSessionID != domain.ID(f.change.Session.Id) {
		t.Fatal("replacement child created")
	}
	output := forkResultFixture(t, input)
	output.Version = 3
	output.Preparation = input.Retry.ChildPreparation
	output.Manifest = input.Retry.ChildManifest
	raw, _ := json.Marshal(output)
	_, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	if f.input.SidechatRetry == nil || f.input.SidechatRetry.GenerationID != domain.ID(request.Mutation.RequestId) || f.input.Fork == nil || f.input.Fork.RuntimeID != input.RuntimeID || f.input.Input.Prompt != "What is the current state?" {
		t.Fatal("question identity or fresh runtime lost")
	}
	f.thread = domain.ID(f.input.Fork.NativeThreadID)
	return input
}
func TestSidechatQuestionRetrySameChildLatestBoundaryReplayAndAtomicAnswer(t *testing.T) {
	parent, child := completedRetrySidechatFixture(t)
	original, _ := store.Decode[domain.Session](child.refresh(t))
	origin, _ := json.Marshal(original.Fork)
	baseline := child.input.ExecutionID
	parent.enqueue(t, "New parent execution", domain.ExecuteMode)
	parent.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	parent.claim(t)
	parent.complete(t, domain.ExecutionSucceeded)
	p2 := parent.turn
	request := retryRequestFixture(t, child)
	if request.ExpectedParentTurnId != string(p2) {
		t.Fatal("did not select latest parent")
	}
	accepted, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request))
	if err != nil {
		t.Fatal(err)
	}
	if accepted.Msg.Replayed {
		t.Fatal("fresh request replayed")
	}
	before := retryViewFixture(t, child, request.Mutation.RequestId)
	if before.CurrentAnswer != baseline || before.Eligible || len(before.Generations) != 1 {
		t.Fatal("early answer replacement or duplicate admission")
	}
	competing := proto.Clone(request).(*pb.RetrySidechatQuestionRequest)
	competing.Mutation.RequestId = string(domain.NewID())
	competing.Mutation.ExpectedRevision = before.ChildRevision
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, competing)); err == nil {
		t.Fatal("two unresolved generations admitted")
	}
	input := finishRetryForkFixture(t, child, request)
	if input.Completion.NativeTurnID != domain.NativeIdentity(p2) {
		t.Fatal("accepted parent boundary changed")
	}
	during := retryViewFixture(t, child, request.Mutation.RequestId)
	if during.CurrentAnswer != baseline {
		t.Fatal("streaming replaced previous answer")
	}
	completeReadOnlySidechat(t, child, domain.ExecutionSucceeded)
	final := retryViewFixture(t, child, request.Mutation.RequestId)
	if final.CurrentAnswer != child.input.ExecutionID || !final.Generations[0].Completed || !final.Eligible {
		t.Fatal("completed answer not selected or repeated retry ineligible", final.Problem)
	}
	session, _ := store.Decode[domain.Session](child.refresh(t))
	afterOrigin, _ := json.Marshal(session.Fork)
	if !bytes.Equal(origin, afterOrigin) || session.Name != original.Name || session.Fork.SourceSessionID != domain.ID(parent.change.Session.Id) {
		t.Fatal("original child ownership amended")
	}
	// Advancing the parent cannot retarget the original generation receipt.
	parent.enqueue(t, "Third parent execution", domain.ExecuteMode)
	parent.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	parent.claim(t)
	parent.complete(t, domain.ExecutionSucceeded)
	replay, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("retry receipt replay", err)
	}
	replayView := retryViewFixture(t, child, request.Mutation.RequestId)
	if len(replayView.Generations) != 1 || replayView.Generations[0].ParentTurnID != domain.NativeIdentity(p2) {
		t.Fatal("replay adopted later parent")
	}
	child.enqueue(t, "A second direct question", domain.ExecuteMode)
	if retryViewFixture(t, child, "").Candidate {
		t.Fatal("second direct question retained eligibility")
	}
	late, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request))
	if err != nil || !late.Msg.Replayed || retryViewFixture(t, child, request.Mutation.RequestId).ObservedGeneration != domain.ID(request.Mutation.RequestId) {
		t.Fatal("later direct question hid original retry receipt", err)
	}

}
func TestSidechatQuestionRetryFailureRetainsAnswerAndCleanupGenerations(t *testing.T) {
	parent, child := completedRetrySidechatFixture(t)
	baseline := child.input.ExecutionID
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	finishRetryForkFixture(t, child, request)
	completeReadOnlySidechat(t, child, domain.ExecutionFailed)
	view := retryViewFixture(t, child, request.Mutation.RequestId)
	if view.CurrentAnswer != baseline || view.Generations[0].Completed || !view.Eligible {
		t.Fatal("settled failure lost previous answer or cleanup", view.Problem)
	}
	before := child.refresh(t)
	deleted, err := sessionClient(child.accountFixture).DeleteSession(context.Background(), ownerRequest(child.identity, &pb.DeleteSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(before.ID), ExpectedRevision: before.Revision}}))
	if err != nil {
		t.Fatal(err)
	}
	_ = deleted
	deletion, err := child.service.Store.GetSessionDeletion(context.Background(), domain.ID(child.change.Session.Id))
	if err != nil {
		t.Fatal(err)
	}
	if len(deletion.Workers) != 1 || len(deletion.Workers[0].Work.RetryForks) != 1 || deletion.Workers[0].Work.Fork == nil {
		t.Fatal("deletion omitted original/retry native ownership")
	}
	if _, err := parent.service.Store.Get(context.Background(), domain.SessionKind, domain.ID(parent.change.Session.Id)); err != nil {
		t.Fatal("child deletion touched parent")
	}
}

func TestSidechatQuestionRetryStopFencesOriginalNativeAdmission(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	baseline := child.input.ExecutionID
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	child.control(t, pb.SessionAction_SESSION_ACTION_STOP)
	view := retryViewFixture(t, child, request.Mutation.RequestId)
	if view.CurrentAnswer != baseline || (view.Phase != domain.JobCanceled && view.Phase != domain.JobClaimed) {
		t.Fatal("Stop lost previous answer or retained queued native work", view)
	}
	g := view.Generations[0]
	r, err := child.service.Store.Get(context.Background(), domain.JobKind, g.ForkJobID)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	if err != nil {
		t.Fatal(err)
	}
	if job.State == domain.JobClaimed {
		err = child.service.Store.Read(context.Background(), func(tx *store.Tx) error {
			canceled, e := tx.JobCancellationRequested(r.ID)
			if e != nil {
				return e
			}
			if !canceled {
				t.Fatal("Stop did not fence original claimed generation")
			}
			var input domain.ForkJobInput
			if domain.Decode(job.Input, &input) != nil {
				t.Fatal("invalid original assignment")
			}
			if validateSidechatRetryForkAuthority(tx, input) == nil {
				t.Fatal("Stop authorized native admission")
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	} else if job.State != domain.JobCanceled || job.InstanceID != "" {
		t.Fatal("stopped unclaimed generation acquired native authority")
	}
}
func TestSidechatQuestionRetryRejectsStaleRevisionsAndOldWorkers(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	request := retryRequestFixture(t, child)
	request.ExpectedParentRevision++
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request)); err == nil {
		t.Fatal("stale parent admitted")
	}
	if len(retryViewFixture(t, child, "").Generations) != 0 {
		t.Fatal("rejected request retained native work")
	}
	_, err := child.service.Store.Mutate(context.Background(), domain.NewID(), "test.retry-old-worker", nil, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.MachineKind, domain.ID(child.machine.Id))
		if e != nil {
			return nil, e
		}
		m, e := store.Decode[domain.Machine](r)
		if e != nil {
			return nil, e
		}
		for i, c := range m.WorkerCapabilities {
			if c == domain.SidechatQuestionRetryV1 {
				m.WorkerCapabilities = append(m.WorkerCapabilities[:i], m.WorkerCapabilities[i+1:]...)
				break
			}
		}
		return tx.Put(domain.MachineKind, r.ID, r.Revision, "", "", m)
	})
	if err != nil {
		t.Fatal(err)
	}
	if retryViewFixture(t, child, "").Eligible {
		t.Fatal("old Worker activated retry")
	}
}
