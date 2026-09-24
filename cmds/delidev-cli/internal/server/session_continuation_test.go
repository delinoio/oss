package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/apiproxy"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

type continuationFixture struct {
	*firstDispatchFixture
	job          *pb.Resource
	input        domain.ExecutionJobInput
	thread, turn domain.ID
}

func newContinuationFixture(t *testing.T, outcome domain.ExecutionOutcome) *continuationFixture {
	t.Helper()
	f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixture(t), thread: domain.NewID()}
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	f.complete(t, outcome)
	return f
}

func (f *continuationFixture) claim(t *testing.T) {
	t.Helper()
	for {
		if !f.workerStream.Receive() {
			t.Fatal("missing continuation assignment", f.workerStream.Err())
		}
		if f.workerStream.Msg().Job != nil {
			break
		}
	}
	f.job, f.turn = f.workerStream.Msg().Job, domain.NewID()
	var job domain.Job
	f.input = domain.ExecutionJobInput{}
	if domain.Decode(f.job.DocumentJson, &job) != nil || domain.Decode(job.Input, &f.input) != nil || f.input.Validate() != nil {
		t.Fatal("invalid claimed assignment")
	}
}

func (f *continuationFixture) publish(t *testing.T, kind domain.ExecutionEventKind, sequence uint64, outcome domain.ExecutionOutcome) *pb.PublishExecutionRequest {
	t.Helper()
	event := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: sequence, Kind: kind, NativeThreadID: string(f.thread), NativeTurnID: string(f.turn), Outcome: outcome}
	if kind == domain.ExecutionThreadBound {
		event.NativeTurnID = ""
		event.Observed = &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}
	}
	raw, _ := json.Marshal(event)
	req := &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}
	if _, err := f.workerClient.PublishExecution(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
		t.Fatal(err)
	}
	return req
}

func (f *continuationFixture) complete(t *testing.T, outcome domain.ExecutionOutcome) *pb.ReportWorkRequest {
	t.Helper()
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	return f.finish(t, outcome)
}

func (f *continuationFixture) finish(t *testing.T, outcome domain.ExecutionOutcome) *pb.ReportWorkRequest {
	t.Helper()
	f.publish(t, domain.ExecutionTurnFinished, 3, outcome)
	completion := domain.ExecutionCompletion{Version: 2, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: f.thread, NativeTurnID: f.turn, LastSequence: 3, Outcome: outcome, CleanupVerified: true, NativeCheckpointDigest: strings.Repeat("ab", 32)}
	raw, _ := json.Marshal(completion)
	req := &pb.ReportWorkRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, OutputJson: raw}
	if _, err := f.workerClient.ReportWork(context.Background(), ownerRequest(f.workerIdentity, req)); err != nil {
		t.Fatal(err)
	}
	return req
}

func (f *continuationFixture) enqueue(t *testing.T, prompt string, mode domain.SessionMode) *pb.Resource {
	t.Helper()
	raw, _ := json.Marshal(domain.SessionInput{Prompt: prompt, Mode: mode})
	r, err := sessionClient(f.accountFixture).EnqueueInput(context.Background(), ownerRequest(f.identity, &pb.EnqueueInputRequest{RequestId: string(domain.NewID()), SessionId: f.change.Session.Id, DocumentJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	return r.Msg.Change.Input
}

func (f *continuationFixture) control(t *testing.T, action pb.SessionAction) *pb.ControlSessionRequest {
	t.Helper()
	r := f.refresh(t)
	req := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(r.ID), ExpectedRevision: r.Revision}, Action: action}
	if _, err := sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, req)); err != nil {
		t.Fatal(err)
	}
	return req
}

func (f *continuationFixture) grant(t *testing.T) string {
	t.Helper()
	token := apiproxy.TokenPrefix + strings.Repeat("x", 32) + string(domain.NewID())
	digest := sha256.Sum256([]byte(token))
	_, err := f.workerClient.RegisterExecution(context.Background(), ownerRequest(f.workerIdentity, &pb.RegisterExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, CredentialDigest: digest[:]}))
	if err != nil {
		t.Fatal(err)
	}
	return token
}

func TestContinuationFIFOFreezesSelectionAndRetainsPredecessor(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	ctx := context.Background()
	state, _ := store.Decode[domain.Session](f.refresh(t))
	initial, _ := json.Marshal(state.InitialExecution)
	previous := *state.Execution
	firstTurn := f.turn
	firstJob, firstInput := f.job, f.input
	second := f.enqueue(t, "second input", domain.ExecuteMode)
	edited, err := sessionClient(f.accountFixture).EditQueuedInput(ctx, ownerRequest(f.identity, &pb.EditQueuedInputRequest{Mutation: acctMutation(second, domain.NewID()), SessionId: f.change.Session.Id, Prompt: "edited second input"}))
	if err != nil {
		t.Fatal(err)
	}
	second = edited.Msg.Change.Input
	third := f.enqueue(t, "third input", domain.PlanMode)
	f.mutateAgent(t, func(a *domain.Agent) { a.Effort = "high"; a.Options.MaxConcurrency = 8; a.Accounts = nil })
	before := f.refresh(t)
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for range 8 {
		wg.Go(func() { results <- f.service.dispatchExecution(ctx, before) })
	}
	wg.Wait()
	close(results)
	claims := 0
	for err := range results {
		if err == nil {
			claims++
		} else if domain.SafeError(err).Code != domain.Conflict {
			t.Fatal(err)
		}
	}
	if claims != 1 {
		t.Fatal("concurrent dispatch claimed more than one turn", claims)
	}
	f.claim(t)
	if f.input.Input.Prompt != "edited second input" {
		t.Fatal("continuation ignored the latest queued edit")
	}
	if f.input.InputID != domain.ID(second.Id) || f.input.ExecutionID == firstInput.ExecutionID || f.input.ThreadRequestID == firstInput.ThreadRequestID || f.input.TurnRequestID == firstInput.TurnRequestID || f.input.Version != 2 || f.input.ConfigurationDigest != firstInput.ConfigurationDigest || f.input.Continuation == nil || f.input.Continuation.Previous.JobID != domain.ID(firstJob.Id) || f.input.Continuation.Previous.LastSequence != previous.LastSequence || f.input.Continuation.Intent != domain.ContinueAutomatically || f.input.AccountID != firstInput.AccountID || f.input.ConnectionID != firstInput.ConnectionID {
		t.Fatal("continuation replaced FIFO, snapshot or predecessor ownership")
	}
	var original domain.Job
	if domain.Decode(firstJob.DocumentJson, &original) != nil || f.input.Continuation.AssignmentInputDigest != continuationDigest(original.Input) {
		t.Fatal("predecessor bytes are not bound")
	}
	state, _ = store.Decode[domain.Session](f.refresh(t))
	retained, _ := json.Marshal(state.InitialExecution)
	if !bytes.Equal(initial, retained) || state.PendingInputs != 2 || state.Execution != nil || state.NextExecutionIntent != "" || state.CurrentExecution == nil {
		t.Fatal("claim consumed queue capacity or changed first selection")
	}
	token := f.grant(t)
	lease, err := f.service.executionAuthority.Acquire(ctx, token)
	if err != nil {
		t.Fatal("new turn lacked scoped authority", err)
	}
	lease.Release()
	completionRequest := f.complete(t, domain.ExecutionSucceeded)
	if _, err := f.service.executionAuthority.Acquire(ctx, token); err == nil {
		t.Fatal("completed turn retained relay authority")
	}
	if err := f.service.dispatchExecution(ctx, f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	if f.input.InputID != domain.ID(third.Id) || f.input.Input.Mode != domain.PlanMode || f.input.Continuation.HistoryExecutionID != firstInput.ExecutionID || f.input.Continuation.Previous.ExecutionID == firstInput.ExecutionID {
		t.Fatal("third turn did not follow the exact latest predecessor")
	}
	beforeReplay := f.refresh(t)
	r, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, completionRequest))
	if err != nil || !r.Msg.Replayed || f.refresh(t).Revision != beforeReplay.Revision {
		t.Fatal("old completion replay changed the successor", err)
	}
	completionRequest.Mutation.RequestId = string(domain.NewID())
	if _, err := f.workerClient.ReportWork(ctx, ownerRequest(f.workerIdentity, completionRequest)); err == nil {
		t.Fatal("new receipt accepted an old completion")
	}
	if _, err := f.service.executionAuthority.Acquire(ctx, token); err == nil {
		t.Fatal("old token authorized successor")
	}
	// A fresh publication cannot bind the root to a changed thread/default or
	// reuse any earlier native turn, including one older than the predecessor.
	for _, bad := range []domain.ExecutionEvent{
		{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(domain.NewID()), Observed: &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "on-request"}},
		{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 1, Kind: domain.ExecutionThreadBound, NativeThreadID: string(f.thread), Observed: &domain.ObservedExecutionSettings{Model: f.input.Configuration.NativeModel, Permission: domain.PermissionReadOnly, ApprovalPolicy: "never"}},
	} {
		raw, _ := json.Marshal(bad)
		_, err := f.workerClient.PublishExecution(ctx, ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
		if err == nil {
			t.Fatal("continuation replaced native root or defaults")
		}
	}
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	badTurn := domain.ExecutionEvent{Version: 1, ExecutionID: f.input.ExecutionID, Sequence: 2, Kind: domain.ExecutionInputAccepted, NativeThreadID: string(f.thread), NativeTurnID: string(firstTurn)}
	raw, _ := json.Marshal(badTurn)
	_, err = f.workerClient.PublishExecution(ctx, ownerRequest(f.workerIdentity, &pb.PublishExecutionRequest{Mutation: acctMutation(f.job, domain.NewID()), MachineId: f.machine.Id, InstanceId: f.workerInstance, EventJson: raw}))
	if err == nil {
		t.Fatal("an older native turn was rebound to fresh input")
	}
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	f.finish(t, domain.ExecutionSucceeded)
	replayed, err := sessionClient(f.accountFixture).CreateSession(ctx, ownerRequest(f.identity, f.request))
	if err != nil || replayed.Msg.Change.ExecutionJob.Id != f.job.Id {
		t.Fatal("old creation receipt failed to join latest execution", err)
	}
}

func TestContinuationExplicitResumeWithQueuedAndFutureInput(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped, domain.ExecutionSucceeded} {
		for _, empty := range []bool{false, true} {
			t.Run(string(outcome)+map[bool]string{false: "/queued", true: "/future"}[empty], func(t *testing.T) {
				f := newContinuationFixture(t, outcome)
				if outcome == domain.ExecutionSucceeded {
					f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
				}
				var queued *pb.Resource
				if !empty {
					queued = f.enqueue(t, "explicit input", domain.PlanMode)
				}
				if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err == nil {
					t.Fatal("paused/failed session automatically continued")
				}
				resume := f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
				if empty {
					state, _ := store.Decode[domain.Session](f.refresh(t))
					if state.Dispatch != domain.DispatchReady || state.NextExecutionIntent != domain.ContinueExplicitly || state.Outcome != outcome {
						t.Fatal("empty Resume fabricated execution or lost intent")
					}
					queued = f.enqueue(t, "future explicit input", domain.ExecuteMode)
					if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
						t.Fatal(err)
					}
				}
				f.claim(t)
				if f.input.InputID != domain.ID(queued.Id) || f.input.Continuation.Intent != domain.ContinueExplicitly {
					t.Fatal("Resume did not bind exact input and intent")
				}
				f.complete(t, domain.ExecutionSucceeded)
				f.control(t, pb.SessionAction_SESSION_ACTION_STOP)
				before := f.refresh(t)
				if _, err := sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, resume)); err != nil {
					t.Fatal(err)
				}
				if f.refresh(t).Revision != before.Revision {
					t.Fatal("old Resume receipt resumed a later Stop")
				}
			})
		}
	}
}

func TestContinuationArchiveTargetsCurrentJobAndRestoreKeepsPause(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionSucceeded)
	firstJob := domain.ID(f.job.Id)
	f.enqueue(t, "second turn", domain.ExecuteMode)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
		t.Fatal(err)
	}
	f.claim(t)
	f.publish(t, domain.ExecutionThreadBound, 1, "")
	f.publish(t, domain.ExecutionInputAccepted, 2, "")
	archive := f.control(t, pb.SessionAction_SESSION_ACTION_ARCHIVE)
	state, _ := store.Decode[domain.Session](f.refresh(t))
	if state.Archive != domain.ArchivePending || state.Dispatch != domain.DispatchPaused || state.NextExecutionIntent != "" || state.ActiveExecutionID != f.input.ExecutionID {
		t.Fatal("Archive lost current native ownership")
	}
	if err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		old, err := tx.JobCancellationRequested(firstJob)
		if err != nil {
			return err
		}
		current, err := tx.JobCancellationRequested(domain.ID(f.job.Id))
		if err == nil && (old || !current) {
			t.Fatal("Archive canceled the wrong native job")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	f.finish(t, domain.ExecutionStopped)
	state, _ = store.Decode[domain.Session](f.refresh(t))
	if state.Archive != domain.Archived || state.ActiveExecutionID != "" || !state.Execution.CleanupVerified {
		t.Fatal("Archive did not complete after current native cleanup")
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_RESTORE)
	before := f.refresh(t)
	state, _ = store.Decode[domain.Session](before)
	if state.Archive != domain.NotArchived || state.Dispatch != domain.DispatchPaused || state.NextExecutionIntent != "" {
		t.Fatal("Restore resumed input")
	}
	if _, err := sessionClient(f.accountFixture).ControlSession(context.Background(), ownerRequest(f.identity, archive)); err != nil {
		t.Fatal(err)
	}
	if f.refresh(t).Revision != before.Revision {
		t.Fatal("old Archive receipt rearchived restored session")
	}
	f.enqueue(t, "restored input", domain.PlanMode)
	if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err == nil {
		t.Fatal("restored session continued automatically")
	}
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	f.complete(t, domain.ExecutionSucceeded)
}

func TestContinuationRejectsChangedReadinessAndUnprovenPredecessor(t *testing.T) {
	for _, scenario := range []string{"legacy-completion", "unfinished-cleanup", "unconfirmed-answer", "waiting", "wrong-turn", "paused", "archived", "recovery", "disabled-account", "exhausted-account", "changed-connection", "unvalidated-account", "offline-worker", "incompatible-model"} {
		t.Run(scenario, func(t *testing.T) {
			f := newContinuationFixture(t, domain.ExecutionSucceeded)
			f.enqueue(t, "retained later input", domain.ExecuteMode)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.continuation-gate", scenario, func(tx *store.Tx) (any, error) {
				r, s, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				switch scenario {
				case "legacy-completion":
					j, err := tx.Get(domain.JobKind, domain.ID(f.job.Id))
					if err != nil {
						return nil, err
					}
					value, _ := store.Decode[domain.Job](j)
					var c domain.ExecutionCompletion
					_ = domain.Decode(value.Output, &c)
					c.Version, c.NativeCheckpointDigest = 1, ""
					value.Output, _ = json.Marshal(c)
					return tx.PutJob(j.ID, j.Revision, j.SessionID, j.ProjectID, value)
				case "unfinished-cleanup":
					s.Execution.CleanupVerified = false
				case "unconfirmed-answer":
					s.Execution.UnconfirmedResponses = 1
				case "waiting":
					s.Execution.Waiting = domain.NativeWaiting{UserInput: true}
				case "wrong-turn":
					s.Execution.NativeTurnID = string(domain.NewID())
				case "paused":
					s.Dispatch = domain.DispatchPaused
				case "archived":
					s.Archive = domain.Archived
				case "recovery":
					s.Recovery = domain.NeedsRecovery
				case "offline-worker":
					return nil, tx.SetWorkerInstance(f.input.MachineID, domain.ID(f.workerInstance), time.Now().UTC().Add(-time.Minute))
				case "incompatible-model":
					mr, err := tx.Get(domain.ModelKind, f.input.Configuration.ModelID)
					if err != nil {
						return nil, err
					}
					model, _ := store.Decode[domain.Model](mr)
					model.Harnesses = []domain.Harness{domain.ClaudeCode}
					return tx.Put(mr.Kind, mr.ID, mr.Revision, "", "", model)
				default:
					ar, a, err := accountFromTx(tx, f.input.AccountID, 0)
					if err != nil {
						return nil, err
					}
					switch scenario {
					case "disabled-account":
						a.Enabled = false
					case "exhausted-account":
						a.ConfirmedExhausted = true
					case "changed-connection":
						a.Connection.ID = domain.NewID()
					case "unvalidated-account":
						a.Validation = nil
					}
					return tx.Put(ar.Kind, ar.ID, ar.Revision, "", "", a)
				}
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, s)
			})
			if err != nil {
				t.Fatal(err)
			}
			before := f.refresh(t)
			if err := f.service.dispatchExecution(context.Background(), before); err == nil {
				t.Fatal("unsafe continuation was claimed")
			}
			state, _ := store.Decode[domain.Session](f.refresh(t))
			if state.CurrentExecution != nil || state.ActiveExecutionID != "" || state.PendingInputs != 1 || state.Execution.ExecutionID != f.input.ExecutionID {
				t.Fatal("failed check consumed or rewrote retained ownership")
			}
			rows, _, err := f.service.Store.Queue(context.Background(), f.input.SessionID, 1, 10)
			if err != nil || len(rows) != 1 {
				t.Fatal("lost queued follow-up", err)
			}
			input, _ := store.Decode[domain.QueuedInput](rows[0])
			if input.Delivery != domain.InputQueued || input.ExecutionID != "" {
				t.Fatal("failed check consumed FIFO")
			}
		})
	}
}
