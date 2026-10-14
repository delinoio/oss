// SPDX-License-Identifier: Apache-2.0
package server

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
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

// pairedClientRetryFixture admits a retry under a paired client device and
// publishes its Fork. The retry execution is delivered but not yet registered.
func pairedClientRetryFixture(t *testing.T, child *continuationFixture) (domain.ID, domain.SidechatRetry, domain.ForkJobInput) {
	t.Helper()
	client := domain.NewID()
	doctorPut(t, child.service, domain.DeviceKind, client, 0, domain.Device{Type: domain.ClientDevice})
	actor := domain.WithPrincipal(context.Background(), domain.Principal{Type: domain.ClientDevice, DeviceID: client})
	request := retryRequestFixture(t, child)
	if _, err := child.service.RetrySidechatQuestion(actor, connect.NewRequest(request)); err != nil {
		t.Fatal(err)
	}
	generation := retryViewFixture(t, child, request.Mutation.RequestId).Generations[0]
	job, input := forkClaimFixture(t, child, string(generation.ForkJobID))
	if input.Actor.DeviceID != client {
		t.Fatal("retry did not record its initiating client")
	}
	reportRetryForkFixture(t, child, job, input)
	return client, generation, input
}

func deliverRetryExecutionFixture(t *testing.T, child *continuationFixture) {
	t.Helper()
	for {
		if !child.workerStream.Receive() {
			t.Fatal("missing retry execution assignment", child.workerStream.Err())
		}
		if job := child.workerStream.Msg().Job; job != nil {
			child.job, child.turn, child.startupToken, child.input = job, domain.NewID(), "", domain.ExecutionJobInput{}
			var envelope domain.Job
			if domain.Decode(job.DocumentJson, &envelope) != nil || envelope.Type != domain.ExecuteSessionJob || domain.Decode(envelope.Input, &child.input) != nil || child.input.SidechatRetry == nil {
				t.Fatal("invalid retry execution assignment")
			}
			return
		}
	}
}

func retryRegistrationRequestFixture(f *continuationFixture) *pb.RegisterExecutionRequest {
	token := apiproxy.TokenPrefix + strings.Repeat("x", 32) + string(domain.NewID())
	digest := sha256.Sum256([]byte(token))
	return &pb.RegisterExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}
}

func registerRetryExecutionFixture(f *continuationFixture) error {
	_, err := f.workerClient.RegisterExecution(context.Background(), ownerRequest(f.workerIdentity, retryRegistrationRequestFixture(f)))
	return err
}

func TestSidechatQuestionRetryRegistrationReplayRechecksRevokedActor(t *testing.T) {
	for _, bound := range []bool{false, true} {
		name := "after registration"
		if bound {
			name = "after thread binding"
		}
		t.Run(name, func(t *testing.T) {
			_, child := completedRetrySidechatFixture(t)
			client, generation, input := pairedClientRetryFixture(t, child)
			deliverRetryExecutionFixture(t, child)
			request := retryRegistrationRequestFixture(child)
			original, err := child.workerClient.RegisterExecution(context.Background(), ownerRequest(child.workerIdentity, request))
			if err != nil || original.Msg.Replayed {
				t.Fatal("original retry grant failed", err)
			}
			if bound {
				child.thread = domain.ID(child.input.Fork.NativeThreadID)
				event := domain.ExecutionEvent{Version: 1, ExecutionID: child.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(child.thread), Observed: &domain.ObservedExecutionSettings{Model: child.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}}
				raw, _ := json.Marshal(event)
				if _, err := child.workerClient.PublishExecution(context.Background(), ownerRequest(child.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(child.job, domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, EventJson: raw})); err != nil {
					t.Fatal("binding did not publish", err)
				}
			}
			before, _ := store.Decode[domain.Session](child.refresh(t))
			device, err := child.service.Store.Get(context.Background(), domain.DeviceKind, client)
			if err != nil {
				t.Fatal(err)
			}
			doctorPut(t, child.service, domain.DeviceKind, client, device.Revision, domain.Device{Type: domain.ClientDevice, Revoked: true})
			if _, err := child.workerClient.RegisterExecution(context.Background(), ownerRequest(child.workerIdentity, request)); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("original receipt replay accepted revoked actor", err)
			}
			after, _ := store.Decode[domain.Session](child.refresh(t))
			if after.SidechatActiveRetry != generation.ID || after.SidechatCurrentAnswer != before.SidechatCurrentAnswer || after.SidechatRetries[0].Fork == nil || after.SidechatRetries[0].Fork.RuntimeID != input.RuntimeID || after.SidechatRetries[0].Completed {
				t.Fatal("denied replay released runtime or changed selected answer")
			}
		})
	}
}

func TestSidechatQuestionRetryRegistrationReplayKeepsOriginalGrant(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	pairedClientRetryFixture(t, child)
	deliverRetryExecutionFixture(t, child)
	request := retryRegistrationRequestFixture(child)
	for attempt := 0; attempt < 2; attempt++ {
		response, err := child.workerClient.RegisterExecution(context.Background(), ownerRequest(child.workerIdentity, request))
		if err != nil || response.Msg.Replayed != (attempt == 1) {
			t.Fatal("valid actor lost exact registration replay", err)
		}
	}
	if err := child.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		grant, err := tx.ExecutionGrant(request.CredentialDigest)
		if err != nil || grant.JobID != domain.ID(child.job.Id) || grant.ExecutionID != child.input.ExecutionID || grant.InstanceID != domain.ID(child.workerInstance) {
			return executionDenied()
		}
		return nil
	}); err != nil {
		t.Fatal("replay replaced original grant", err)
	}
}

func TestSidechatQuestionRetryExecutionRechecksInitiatingActor(t *testing.T) {
	t.Run("revoked actor cannot claim the retained question", func(t *testing.T) {
		_, child := completedRetrySidechatFixture(t)
		client, generation, input := pairedClientRetryFixture(t, child)
		deliverRetryExecutionFixture(t, child)
		record, err := child.service.Store.Get(context.Background(), domain.DeviceKind, client)
		if err != nil {
			t.Fatal(err)
		}
		doctorPut(t, child.service, domain.DeviceKind, client, record.Revision, domain.Device{Type: domain.ClientDevice, Revoked: true})
		if err := registerRetryExecutionFixture(child); err == nil {
			t.Fatal("revoked initiating actor claimed native execution")
		}
		// The accepted runtime stays recorded for cleanup and the generation stays active.
		session, _ := store.Decode[domain.Session](child.refresh(t))
		g := session.SidechatRetries[0]
		if session.SidechatActiveRetry != generation.ID || g.Fork == nil || g.Fork.RuntimeID != input.RuntimeID || g.Completed {
			t.Fatal("revocation released the original runtime owner", g)
		}
	})
	t.Run("unrevoked actor proceeds once", func(t *testing.T) {
		_, child := completedRetrySidechatFixture(t)
		pairedClientRetryFixture(t, child)
		deliverRetryExecutionFixture(t, child)
		if err := registerRetryExecutionFixture(child); err != nil {
			t.Fatal("valid initiating actor was denied", err)
		}
	})
	t.Run("mismatched origin denies", func(t *testing.T) {
		_, child := completedRetrySidechatFixture(t)
		_, generation, _ := pairedClientRetryFixture(t, child)
		deliverRetryExecutionFixture(t, child)
		grant := store.ExecutionGrant{JobID: domain.ID(child.job.Id), ExecutionID: child.input.ExecutionID, MachineID: domain.ID(child.machine.Id), InstanceID: domain.ID(child.workerInstance)}
		check := func(input domain.ExecutionJobInput) error {
			return child.service.Store.Read(context.Background(), func(tx *store.Tx) error {
				return requireSidechatRetryExecutionOrigin(tx, grant, input)
			})
		}
		if err := check(child.input); err != nil {
			t.Fatal("exact origin denied", err)
		}
		cases := map[string]func(*domain.ExecutionJobInput){
			"missing job": func(i *domain.ExecutionJobInput) { fork := *i.Fork; fork.JobID = domain.NewID(); i.Fork = &fork },
			"wrong job": func(i *domain.ExecutionJobInput) {
				fork := *i.Fork
				fork.JobID = generation.ForkJobID
				i.Fork = &fork
				i.SessionID = domain.NewID()
			},
			"other generation": func(i *domain.ExecutionJobInput) {
				retry := *i.SidechatRetry
				retry.GenerationID = domain.NewID()
				i.SidechatRetry = &retry
			},
			"stale instance": func(i *domain.ExecutionJobInput) {
				retry := *i.SidechatRetry
				retry.WorkerInstanceID = domain.NewID()
				i.SidechatRetry = &retry
			},
			"other device": func(i *domain.ExecutionJobInput) {
				retry := *i.SidechatRetry
				retry.WorkerDeviceID = domain.NewID()
				i.SidechatRetry = &retry
			},
			"other runtime": func(i *domain.ExecutionJobInput) { fork := *i.Fork; fork.RuntimeID = domain.NewID(); i.Fork = &fork },
			"other question": func(i *domain.ExecutionJobInput) {
				retry := *i.SidechatRetry
				retry.QuestionID = domain.NewID()
				i.SidechatRetry = &retry
			},
			"not a retry": func(i *domain.ExecutionJobInput) { i.SidechatRetry = nil },
		}
		for name, mutate := range cases {
			changed := child.input
			mutate(&changed)
			if check(changed) == nil {
				t.Fatal("mismatched origin accepted:", name)
			}
		}
		// A Fork job whose recorded actor no longer exists cannot authorize the execution.
		_, err := child.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.retry-foreign-actor", nil, func(tx *store.Tx) (any, error) {
			r, e := tx.Get(domain.JobKind, generation.ForkJobID)
			if e != nil {
				return nil, e
			}
			job, e := store.Decode[domain.Job](r)
			var fork domain.ForkJobInput
			if e != nil || domain.Decode(job.Input, &fork) != nil {
				return nil, e
			}
			fork.Actor.DeviceID = domain.NewID()
			job.Input, e = json.Marshal(fork)
			if e != nil {
				return nil, e
			}
			return tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		})
		if err != nil {
			t.Fatal(err)
		}
		if check(child.input) == nil {
			t.Fatal("unknown initiating actor accepted")
		}
	})
}

// completeChildRecoveryFixture claims the accepted recovery job through the
// original Worker identity and reports exact completion evidence for it.
func completeChildRecoveryFixture(t *testing.T, child *continuationFixture, resource *pb.Resource) {
	t.Helper()
	var claimed store.Record
	var expected domain.ExecutionRecoveryRequest
	_, err := child.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claim-child-recovery", resource.Id, func(tx *store.Tx) (any, error) {
		r, e := tx.Get(domain.JobKind, domain.ID(resource.Id))
		if e != nil {
			return nil, e
		}
		job, e := store.Decode[domain.Job](r)
		if e != nil || domain.Decode(job.Input, &expected) != nil {
			return nil, e
		}
		job.State, job.InstanceID, job.AssignedDeviceID = domain.JobClaimed, domain.ID(child.workerInstance), child.workerDevice
		claimed, e = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		return nil, e
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := expected.Completion
	completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	raw, _ := json.Marshal(domain.ExecutionRecoveryEvidence{Version: 1, JobID: expected.JobID, ReportID: domain.NewID(), Completion: completion})
	if _, err := child.workerClient.ReportWork(context.Background(), ownerRequest(child.workerIdentity, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: string(claimed.ID), ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: child.machine.Id, InstanceId: child.workerInstance, OutputJson: raw})); err != nil {
		t.Fatal(err)
	}
}

func recoverChildExecutionFixture(t *testing.T, child *continuationFixture, execution domain.ID) {
	t.Helper()
	current := child.refresh(t)
	response, err := sessionClient(child.accountFixture).RecoverSessionExecution(context.Background(), ownerRequest(child.identity, &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{Id: string(current.ID), RequestId: string(domain.NewID()), ExpectedRevision: current.Revision}, ExpectedExecutionId: string(execution)}))
	if err != nil {
		t.Fatal("recovery rejected", err)
	}
	completeChildRecoveryFixture(t, child, response.Msg.Change.ExecutionRecoveryJob)
}

func TestSidechatQuestionRetryRecoveryOfOlderGenerationKeepsNewerForkOwner(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	requestA, generationA := acceptRetryFixture(t, child)
	finishRetryForkFixture(t, child, requestA)
	completeReadOnlySidechat(t, child, domain.ExecutionSucceeded)
	executionA := child.input.ExecutionID
	// Generation B fails before any execution exists. Its private runtime owner
	// stays active while the selected execution remains generation A.
	requestB, generationB := acceptRetryFixture(t, child)
	job, _ := forkClaimFixture(t, child, string(generationB.ForkJobID))
	if _, err := child.workerClient.ReportWork(context.Background(), ownerRequest(child.workerIdentity, &pb.ReportWorkRequest{Mutation: acctMutation(job, domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, Problem: &pb.ErrorDetail{Code: string(domain.RecoveryRequired)}})); err != nil {
		t.Fatal(err)
	}
	before, _ := store.Decode[domain.Session](child.refresh(t))
	if before.SidechatActiveRetry != generationB.ID || before.Recovery != domain.NeedsRecovery || before.ExecutionSelection().ID != executionA {
		t.Fatal("fixture did not leave generation B as the active owner", before.SidechatActiveRetry)
	}
	recoverChildExecutionFixture(t, child, executionA)
	after, _ := store.Decode[domain.Session](child.refresh(t))
	if after.SidechatActiveRetry != generationB.ID || after.Dispatch != domain.DispatchPaused || after.Problem == nil {
		t.Fatal("older generation recovery released the newer owner", after.SidechatActiveRetry)
	}
	if !after.SidechatRetries[0].Completed || after.SidechatCurrentAnswer != executionA {
		t.Fatal("older generation recovery lost its own answer")
	}
	view := retryViewFixture(t, child, "")
	if view.Eligible || view.ObservedGeneration != generationB.ID || view.Phase != domain.JobUncertain {
		t.Fatal("newer owner is no longer observed as active", view.Phase)
	}
	// No third generation, direct input or Resume may bypass the retained owner.
	third := &pb.RetrySidechatQuestionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(child.change.Session.Id), ExpectedRevision: view.ChildRevision}, QuestionId: string(view.QuestionID), ExpectedQuestionRevision: view.QuestionRevision, ExpectedParentRevision: view.ParentRevision, ExpectedParentTurnId: string(view.ParentTurnID)}
	if _, err := sessionClient(child.accountFixture).RetrySidechatQuestion(context.Background(), ownerRequest(child.identity, third)); err == nil {
		t.Fatal("third generation admitted while the newer owner is active")
	}
	raw, _ := json.Marshal(domain.SessionInput{Prompt: "Input behind the retained owner", Mode: domain.ExecuteMode})
	if _, err := sessionClient(child.accountFixture).EnqueueInput(context.Background(), ownerRequest(child.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: child.change.Session.Id, DocumentJson: raw})); err == nil {
		t.Fatal("input admitted while the newer owner is active")
	}
	// Replaying the older recovery or stopping the session cannot transfer the
	// newer generation's cleanup ownership.
	current := child.refresh(t)
	if _, err := sessionClient(child.accountFixture).ControlSession(context.Background(), ownerRequest(child.identity, &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(current.ID), ExpectedRevision: current.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESUME})); err == nil {
		t.Fatal("Resume admitted while the newer owner is active")
	}
	final, _ := store.Decode[domain.Session](child.refresh(t))
	if final.SidechatActiveRetry != generationB.ID || final.SidechatRetries[0].ID != generationA.ID || requestB.Mutation.RequestId != string(generationB.ID) {
		t.Fatal("retained owner changed", final.SidechatActiveRetry)
	}
}

func TestSidechatQuestionRetryRecoveryOfSameGenerationStillReleasesItsOwner(t *testing.T) {
	_, child := completedRetrySidechatFixture(t)
	request, generation := acceptRetryFixture(t, child)
	finishRetryForkFixture(t, child, request)
	child.grant(t)
	event := domain.ExecutionEvent{Version: 1, ExecutionID: child.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(child.thread), Observed: &domain.ObservedExecutionSettings{Model: child.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}}
	raw, _ := json.Marshal(event)
	if _, err := child.workerClient.PublishExecution(context.Background(), ownerRequest(child.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(child.job, domain.NewID()), MachineId: child.machine.Id, InstanceId: child.workerInstance, EventJson: raw})); err != nil {
		t.Fatal(err)
	}
	child.publish(t, domain.ExecutionInputAccepted, 2, "")
	child.publish(t, domain.ExecutionTurnFinished, 3, domain.ExecutionSucceeded)
	// Lose only the completion receipt so the retry execution itself needs recovery.
	_, err := child.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.retry-recovery-uncertain", nil, func(tx *store.Tx) (any, error) {
		original, err := tx.Get(domain.JobKind, domain.ID(child.job.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](original)
		if err != nil {
			return nil, err
		}
		job.State, job.Problem = domain.JobUncertain, nativeCompletionUncertain()
		if err := finishLostNativeExecution(tx, original, job); err != nil {
			return nil, err
		}
		return tx.PutJob(original.ID, original.Revision, original.SessionID, original.ProjectID, job)
	})
	if err != nil {
		t.Fatal(err)
	}
	if session, _ := store.Decode[domain.Session](child.refresh(t)); session.SidechatActiveRetry != generation.ID {
		t.Fatal("uncertain execution did not retain its owner")
	}
	recoverChildExecutionFixture(t, child, child.input.ExecutionID)
	session, _ := store.Decode[domain.Session](child.refresh(t))
	if session.SidechatActiveRetry != "" || session.Recovery != domain.NoRecovery || !session.SidechatRetries[0].Completed {
		t.Fatal("same-owner recovery did not release its own generation", session.SidechatActiveRetry)
	}
}
