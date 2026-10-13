// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// acceptRetryFixture admits one retry generation and returns its request, Fork
// job identity and the unchanged accepted Fork input from the durable job.
func acceptRetryFixture(t *testing.T, child *continuationFixture) (*pb.RetrySidechatQuestionRequest, domain.SidechatRetry) {
	t.Helper()
	request := retryRequestFixture(t, child)
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, request)); err != nil {
		t.Fatal(err)
	}
	view := retryViewFixture(t, child, request.Mutation.RequestId)
	return request, view.Generations[len(view.Generations)-1]
}

func storedForkInputFixture(t *testing.T, f *continuationFixture, id domain.ID) domain.ForkJobInput {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.JobKind, id)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](r)
	var input domain.ForkJobInput
	if err != nil || domain.Decode(job.Input, &input) != nil {
		t.Fatal("invalid stored Fork input")
	}
	return input
}

// advanceParentPastRetry queues and selects another parent execution while the
// accepted retry Fork is still unreported. A Worker runs one native job at a
// time, so the new parent turn stays queued behind the Fork; its selection is
// what moves the parent's current execution away from the accepted turn.
func advanceParentPastRetry(t *testing.T, parent *continuationFixture) {
	t.Helper()
	before := parent.refresh(t)
	parent.enqueue(t, "Parent execution after the retry was accepted", domain.ExecuteMode)
	parent.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	after, err := store.Decode[domain.Session](parent.refresh(t))
	previous, _ := store.Decode[domain.Session](before)
	if err != nil || after.ExecutionSelection().ID == previous.ExecutionSelection().ID {
		t.Fatal("parent did not select another execution", err)
	}
}

func retryAuthorityFixture(t *testing.T, f *continuationFixture, input domain.ForkJobInput) error {
	t.Helper()
	return f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		return validateSidechatRetryForkAuthority(tx, input)
	})
}

func reportRetryForkFixture(t *testing.T, child *continuationFixture, job *pb.Resource, input domain.ForkJobInput) {
	t.Helper()
	output := forkResultFixture(t, input)
	output.Version = 3
	output.Preparation, output.Manifest = input.Retry.ChildPreparation, input.Retry.ChildManifest
	raw, _ := json.Marshal(output)
	if _, err := child.workerClient.ReportWork(context.Background(), ownerRequest(child.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
}

func TestSidechatQuestionRetryKeepsAcceptedParentTurnAfterParentAdvances(t *testing.T) {
	parent, child := completedRetrySidechatFixture(t)
	request, generation := acceptRetryFixture(t, child)
	accepted := parent.turn
	input := storedForkInputFixture(t, child, generation.ForkJobID)
	if input.Completion.NativeTurnID != domain.NativeIdentity(accepted) || request.ExpectedParentTurnId != string(accepted) {
		t.Fatal("retry did not accept the parent turn")
	}
	advanceParentPastRetry(t, parent)
	// Claim boundary: the frozen assignment no longer is the parent's current
	// selection, but the accepted turn remains valid history.
	if err := retryAuthorityFixture(t, child, input); err != nil {
		t.Fatal("advanced parent invalidated accepted turn at claim", err)
	}
	// Ordinary current-selection source checks must not accept the older turn.
	err := child.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		pr, source, e := sessionRecord(tx, input.SourceSessionID)
		if e != nil {
			return e
		}
		_, machine, e := activeMachine(tx, source.MachineID)
		if e != nil {
			return e
		}
		return checkedExecutionSource(tx, pr, source, machine, input.SourceAssignment)
	})
	if err == nil {
		t.Fatal("ordinary continuation accepted a non-current execution")
	}
	// Changed original authority still rejects after the parent advanced.
	mutations := map[string]func(*domain.ForkJobInput){
		"account":    func(i *domain.ForkJobInput) { i.SourceAssignment.AccountID = domain.NewID() },
		"worker":     func(i *domain.ForkJobInput) { i.Retry.WorkerInstanceID = domain.NewID() },
		"workspace":  func(i *domain.ForkJobInput) { i.SourceAssignment.Preparation = json.RawMessage(`{}`) },
		"completion": func(i *domain.ForkJobInput) { i.Completion.LastSequence++ },
		"job":        func(i *domain.ForkJobInput) { i.SourceJobID = domain.NewID() },
		"machine":    func(i *domain.ForkJobInput) { i.SourceAssignment.MachineID = domain.NewID() },
	}
	for name, mutate := range mutations {
		changed := input
		retry := *input.Retry
		changed.Retry = &retry
		mutate(&changed)
		if retryAuthorityFixture(t, child, changed) == nil {
			t.Fatal("changed original authority accepted:", name)
		}
	}
	// Publication boundary after the parent advanced keeps exactly the accepted
	// prefix and queues the retained question against the same child.
	fork, claimedInput := forkClaimFixture(t, child, string(generation.ForkJobID))
	reportRetryForkFixture(t, child, fork, claimedInput)
	view := retryViewFixture(t, child, request.Mutation.RequestId)
	if view.Generations[0].ExecutionJobID == "" || view.Generations[0].ParentTurnID != domain.NativeIdentity(accepted) {
		t.Fatal("advanced parent prevented publication of the accepted turn", view.Problem)
	}
	r, err := child.service.Store.Get(context.Background(), domain.JobKind, view.Generations[0].ExecutionJobID)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := store.Decode[domain.Job](r)
	var execution domain.ExecutionJobInput
	if err != nil || domain.Decode(queued.Input, &execution) != nil || execution.Fork == nil || execution.Fork.NativeTurnID != input.Completion.NativeTurnID || execution.SidechatRetry == nil || execution.SidechatRetry.GenerationID != generation.ID {
		t.Fatal("retry execution retargeted the parent turn", err)
	}
}

// interveneChildFixture advances the child revision with an ordinary lifecycle
// write that is not part of the accepted retry admission.
func interveneChildFixture(t *testing.T, child *continuationFixture, kind string) {
	t.Helper()
	current := child.refresh(t)
	mutation := &pb.Mutation{Id: string(current.ID), ExpectedRevision: current.Revision, RequestId: string(domain.NewID())}
	client := sessionClient(child.accountFixture)
	var err error
	switch kind {
	case "rename":
		_, err = client.RenameSession(context.Background(), ownerRequest(child.identity, &pb.RenameSessionRequest{Mutation: mutation, Name: "Renamed after retry admission"}))
	case "budget":
		_, err = client.SetSessionBudget(context.Background(), ownerRequest(child.identity, &pb.SetSessionBudgetRequest{Mutation: mutation, Change: &pb.SetSessionBudgetRequest_Budget{Budget: &pb.EstimatedCostBudget{Currency: "USD", Threshold: "5"}}}))
	default:
		t.Fatal("unknown intervention", kind)
	}
	if err != nil {
		t.Fatal(kind, err)
	}
}

func TestSidechatQuestionRetryRequiresExactPostAdmissionChildRevision(t *testing.T) {
	for _, kind := range []string{"rename", "budget"} {
		t.Run(kind+" before claim", func(t *testing.T) {
			_, child := completedRetrySidechatFixture(t)
			_, generation := acceptRetryFixture(t, child)
			input := storedForkInputFixture(t, child, generation.ForkJobID)
			if err := retryAuthorityFixture(t, child, input); err != nil {
				t.Fatal("exact post-admission revision rejected", err)
			}
			interveneChildFixture(t, child, kind)
			if retryAuthorityFixture(t, child, input) == nil {
				t.Fatal("intervening revision renewed retry authority at claim")
			}
		})
		t.Run(kind+" before publication", func(t *testing.T) {
			_, child := completedRetrySidechatFixture(t)
			request, generation := acceptRetryFixture(t, child)
			job, input := forkClaimFixture(t, child, string(generation.ForkJobID))
			interveneChildFixture(t, child, kind)
			changed, _ := store.Decode[domain.Session](child.refresh(t))
			reportRetryForkFixture(t, child, job, input)
			view := retryViewFixture(t, child, request.Mutation.RequestId)
			g := view.Generations[0]
			if g.ExecutionJobID != "" || g.ExecutionID != "" || g.Completed {
				t.Fatal("rejected retry reached native execution")
			}
			// The claimed runtime stays owned for deletion cleanup.
			if g.Fork == nil || g.Fork.RuntimeID != input.RuntimeID || g.ID != generation.ID {
				t.Fatal("rejected retry lost its claimed runtime owner", g)
			}
			after, _ := store.Decode[domain.Session](child.refresh(t))
			sameBudget := reflect.DeepEqual(after.EstimatedCostBudget, changed.EstimatedCostBudget)
			if !sameBudget || after.Recovery != domain.NeedsRecovery || after.SidechatActiveRetry != generation.ID || after.Name != changed.Name || after.Fork == nil || after.CurrentExecution != nil && changed.CurrentExecution != nil && *after.CurrentExecution != *changed.CurrentExecution {
				t.Fatal("rejected retry retargeted child metadata or released ownership")
			}
			if retryViewFixture(t, child, request.Mutation.RequestId).Phase != domain.JobUncertain {
				t.Fatal("rejected publication was not retained as uncertain")
			}
		})
	}
}
