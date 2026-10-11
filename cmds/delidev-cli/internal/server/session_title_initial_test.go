package server

import (
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

func titleJobCount(t *testing.T, service *Service, sessionID domain.ID) int {
	t.Helper()
	rows, err := service.Store.List(context.Background(), store.Filter{Kind: domain.JobKind, SessionID: sessionID, Limit: 200})
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range rows {
		job, err := store.Decode[domain.Job](row)
		if err != nil {
			t.Fatal(err)
		}
		if job.Type == domain.GenerateSessionTitleJob {
			count++
		}
	}
	return count
}

func TestInitialTitleFailureCannotBeRevivedBySuccessfulContinuation(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		for _, legacyWaiting := range []bool{false, true} {
			name := string(outcome)
			if legacyWaiting {
				name += "/legacy-waiting"
			}
			t.Run(name, func(t *testing.T) {
				base := newFirstDispatchFixtureForHarness(t, domain.Codex)
				f := &continuationFixture{firstDispatchFixture: base, thread: domain.NewID()}
				if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
					t.Fatal(err)
				}
				f.claim(t)
				pf := &publicationFixture{authorityFixture: &authorityFixture{service: f.service, input: f.input}}
				setAutomaticTitleWaiting(t, pf)
				f.complete(t, outcome)
				initial := readTitleBudgetSession(t, pf)
				wantState, wantReason := domain.TitleFailed, domain.TitleReasonInferenceFailed
				if outcome == domain.ExecutionStopped {
					wantState, wantReason = domain.TitleSkipped, domain.TitleReasonCanceled
				}
				if initial.TitleState != wantState || initial.TitleReason != wantReason || initial.TitleOperationID == "" || initial.TitleJobID != "" {
					t.Fatalf("initial failure left naming unsettled: %+v", initial)
				}
				if legacyWaiting {
					// Older retained sessions can still be waiting after a failed turn.
					// Continuation success must not manufacture initial authority.
					setAutomaticTitleWaiting(t, pf)
					initial = readTitleBudgetSession(t, pf)
				}
				f.enqueue(t, "Follow-up after the failed first input", domain.PlanMode)
				f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
				f.claim(t)
				if f.input.Continuation == nil {
					t.Fatal("fixture did not dispatch a continuation")
				}
				f.complete(t, domain.ExecutionSucceeded)
				after := readTitleBudgetSession(t, pf)
				if after.TitleState != initial.TitleState || after.TitleReason != initial.TitleReason || after.TitleOperationID != initial.TitleOperationID || after.TitleJobID != "" || titleJobCount(t, f.service, f.input.SessionID) != 0 {
					t.Fatalf("continuation changed initial naming: before=%+v after=%+v", initial, after)
				}
			})
		}
	}
}

func TestInitialTitleSuccessQueuesOnceAndReplaysCompletion(t *testing.T) {
	f := publicationFixtureFromAuthority(t, newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) {
		input.Installation.ResolvedPath = "/private/codex"
	}, false))
	f.registerGrant(t)
	if _, err := f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1}})); err != nil {
		t.Fatal(err)
	}
	setAutomaticTitleWaiting(t, f)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	terminal := f.event(domain.ExecutionTurnFinished, 3)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	request, _ := f.reportCompletion(t, f.completion())
	initial := readTitleBudgetSession(t, f)
	if initial.TitleState != domain.TitleQueued || initial.TitleOperationID == "" || initial.TitleJobID == "" || titleJobCount(t, f.service, f.input.SessionID) != 1 {
		t.Fatalf("successful first turn did not queue exactly one title: %+v", initial)
	}
	replay, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("completion receipt did not replay", err)
	}
	after := readTitleBudgetSession(t, f)
	if after.TitleOperationID != initial.TitleOperationID || after.TitleJobID != initial.TitleJobID || titleJobCount(t, f.service, f.input.SessionID) != 1 {
		t.Fatal("completion replay created another title")
	}
}

func TestInitialTitleCleanupUncertaintyAndManualNamesRemainUntouched(t *testing.T) {
	for _, manual := range []bool{false, true} {
		name := "uncertain"
		if manual {
			name = "manual"
		}
		t.Run(name, func(t *testing.T) {
			f := newPublicationFixture(t)
			if !manual {
				setAutomaticTitleWaiting(t, f)
			}
			before := readTitleBudgetSession(t, f)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			terminal := f.event(domain.ExecutionTurnFinished, 3)
			terminal.Outcome = domain.ExecutionFailed
			f.publish(t, terminal)
			completion := f.completion()
			completion.Outcome, completion.CleanupVerified = domain.ExecutionFailed, manual
			f.reportCompletion(t, completion)
			after := readTitleBudgetSession(t, f)
			if after.Name != before.Name || after.NameOwner != before.NameOwner || after.TitleState != before.TitleState || after.TitleOperationID != before.TitleOperationID || titleJobCount(t, f.service, f.input.SessionID) != 0 {
				t.Fatalf("uncertain cleanup or manual naming gained title work: %+v", after)
			}
			if !manual && (after.Recovery != domain.NeedsRecovery || after.Execution.CleanupVerified || after.Problem == nil) {
				t.Fatal("unverified cleanup lost visible recovery state")
			}
		})
	}
}

func TestRecoveredInitialFailureSettlesWithoutTitleJob(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := recoveryFixture(t, outcome)
			setAutomaticTitleWaiting(t, f)
			_, change := acceptRecovery(t, f)
			completeRecovery(t, f, change.ExecutionRecoveryJob)
			session := readTitleBudgetSession(t, f)
			wantState, wantReason := domain.TitleFailed, domain.TitleReasonInferenceFailed
			if outcome == domain.ExecutionStopped {
				wantState, wantReason = domain.TitleSkipped, domain.TitleReasonCanceled
			}
			if session.TitleState != wantState || session.TitleReason != wantReason || session.TitleOperationID == "" || session.TitleJobID != "" || titleJobCount(t, f.service, f.input.SessionID) != 0 || session.Recovery != domain.NoRecovery || !session.Execution.CleanupVerified {
				t.Fatalf("recovered failure did not settle initial title: %+v", session)
			}
		})
	}
}

func TestInitialTitleAdmissionPreservesNoSendRetryAndRejectsOtherInputs(t *testing.T) {
	f := newPublicationFixture(t)
	setAutomaticTitleWaiting(t, f)
	session := readTitleBudgetSession(t, f)
	if !pendingInitialSessionTitle(session, f.input) {
		t.Fatal("original initial execution lost title authority")
	}
	retry := f.input
	retry.ExecutionID, retry.InputID = domain.NewID(), domain.NewID()
	retry.Retry = &domain.ExecutionStartupRetry{JobID: f.job, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID}
	session.NativeExecutionRootID = retry.ExecutionID
	if !pendingInitialSessionTitle(session, retry) {
		t.Fatal("proven no-send initial retry lost title authority")
	}
	for _, change := range []func(*domain.ExecutionJobInput){
		func(input *domain.ExecutionJobInput) { input.Continuation = &domain.ExecutionContinuation{} },
		func(input *domain.ExecutionJobInput) { input.Retry = nil },
		func(input *domain.ExecutionJobInput) { input.AccountID = domain.NewID() },
		func(input *domain.ExecutionJobInput) { input.ExecutionID = domain.NewID() },
	} {
		foreign := retry
		change(&foreign)
		if pendingInitialSessionTitle(session, foreign) {
			t.Fatal("foreign or continuation input gained initial title authority")
		}
		before := session
		if err := queueAutomaticSessionTitle(nil, store.Record{}, &session, store.Record{}, foreign, true); err != nil || session.TitleOperationID != before.TitleOperationID {
			t.Fatal("ineligible input reached title allocation", err)
		}
		settleUnsuccessfulInitialTitle(&session, foreign, domain.ExecutionFailed)
		if session.TitleState != before.TitleState || session.TitleOperationID != before.TitleOperationID {
			t.Fatal("ineligible input settled the original title")
		}
	}
}
