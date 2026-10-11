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

func TestSessionTitleInitialFailureCannotBeRetriedByContinuation(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := &continuationFixture{firstDispatchFixture: newFirstDispatchFixture(t), thread: domain.NewID()}
			if err := f.service.dispatchExecution(context.Background(), f.refresh(t)); err != nil {
				t.Fatal(err)
			}
			f.claim(t)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.initial-title", nil, func(tx *store.Tx) (any, error) {
				r, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				session.NameMode, session.NameOwner, session.NameGeneration = domain.AutomaticSessionName, domain.AutomaticNameOwner, 1
				session.TitleState = domain.TitleWaiting
				return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
			})
			if err != nil {
				t.Fatal(err)
			}
			f.complete(t, outcome)
			before, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil {
				t.Fatal(err)
			}
			state, reason := domain.TitleFailed, domain.TitleReasonInferenceFailed
			if outcome == domain.ExecutionStopped {
				state, reason = domain.TitleSkipped, domain.TitleReasonCanceled
			}
			if before.TitleState != state || before.TitleReason != reason || before.TitleOperationID != "" || before.TitleJobID != "" {
				t.Fatalf("initial outcome allocated or stranded title work: %+v", before)
			}
			f.enqueue(t, "successful continuation", domain.PlanMode)
			f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
			f.claim(t)
			f.complete(t, domain.ExecutionSucceeded)
			after, err := store.Decode[domain.Session](f.refresh(t))
			if err != nil || after.TitleState != state || after.TitleReason != reason || after.TitleOperationID != "" || after.TitleJobID != "" || after.Outcome != domain.ExecutionSucceeded {
				t.Fatalf("continuation changed initial title settlement: %+v %v", after, err)
			}
		})
	}
}

func TestSessionTitleLegacyWaitingContinuationAllocatesNothing(t *testing.T) {
	f := newContinuationFixture(t, domain.ExecutionFailed)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.legacy-waiting-title", nil, func(tx *store.Tx) (any, error) {
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.NameMode, session.NameOwner, session.NameGeneration = domain.AutomaticSessionName, domain.AutomaticNameOwner, 1
		session.TitleState = domain.TitleWaiting
		return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
	f.enqueue(t, "later successful input", domain.PlanMode)
	f.control(t, pb.SessionAction_SESSION_ACTION_RESUME)
	f.claim(t)
	f.complete(t, domain.ExecutionSucceeded)
	session, err := store.Decode[domain.Session](f.refresh(t))
	if err != nil || session.TitleOperationID != "" || session.TitleJobID != "" {
		t.Fatalf("continuation allocated initial title work: %+v %v", session, err)
	}
}

func TestExecutionRecoverySettlesUnsuccessfulInitialTitleWithoutOperation(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := recoveryFixture(t, outcome)
			setAutomaticTitleWaiting(t, f)
			_, change := acceptRecovery(t, f)
			completeRecovery(t, f, change.ExecutionRecoveryJob)
			session := readTitleBudgetSession(t, f)
			state, reason := domain.TitleFailed, domain.TitleReasonInferenceFailed
			if outcome == domain.ExecutionStopped {
				state, reason = domain.TitleSkipped, domain.TitleReasonCanceled
			}
			if session.TitleState != state || session.TitleReason != reason || session.TitleOperationID != "" || session.TitleJobID != "" || session.Recovery != domain.NoRecovery {
				t.Fatalf("recovery failed initial title settlement: %+v", session)
			}
		})
	}
}

func TestSessionTitleInitialSuccessQueuesExactlyOnceAndPreservesManualNames(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		t.Run(map[bool]string{true: "automatic", false: "manual"}[automatic], func(t *testing.T) {
			f := publicationFixtureFromAuthority(t, newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) { input.Installation.ResolvedPath = "/private/codex" }, false))
			f.registerGrant(t)
			if _, err := f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{ProtocolVersion: 2, RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1}})); err != nil {
				t.Fatal(err)
			}
			if automatic {
				setAutomaticTitleWaiting(t, f)
			}
			before := readTitleBudgetSession(t, f)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			terminal := f.event(domain.ExecutionTurnFinished, 3)
			terminal.Outcome = domain.ExecutionSucceeded
			f.publish(t, terminal)
			request, _ := f.reportCompletion(t, f.completion())
			if _, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request)); err != nil {
				t.Fatal(err)
			}
			session := readTitleBudgetSession(t, f)
			records, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.JobKind, SessionID: f.input.SessionID, Limit: 100})
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, record := range records {
				job, err := store.Decode[domain.Job](record)
				if err != nil {
					t.Fatal(err)
				}
				if job.Type == domain.GenerateSessionTitleJob {
					count++
				}
			}
			if automatic {
				if count != 1 || session.TitleState != domain.TitleQueued || session.TitleOperationID == "" || session.TitleJobID == "" {
					t.Fatalf("initial success did not queue exactly one title: %+v count=%d", session, count)
				}
			} else if count != 0 || session.Name != before.Name || session.NameOwner != before.NameOwner || session.TitleOperationID != "" || session.TitleJobID != "" {
				t.Fatalf("successful execution changed manual naming: %+v count=%d", session, count)
			}
		})
	}
}
