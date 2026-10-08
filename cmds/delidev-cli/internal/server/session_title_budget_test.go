package server

import (
	"context"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
)

// Retained evidence uses the real pricing/usage store operations. These fixtures
// exercise title settlement, independently of native telemetry acceptance.
func seedTitleBudget(t *testing.T, f *publicationFixture, threshold string) {
	t.Helper()
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.title-budget", threshold, func(tx *store.Tx) (any, error) {
		basis, err := tokenPricing(publicPrice("USD"))
		if err != nil {
			return nil, err
		}
		if _, err = tx.PutPricing(f.input.Configuration.ModelID, 0, domain.NewID(), basis); err != nil {
			return nil, err
		}
		counts := reportedCounts(18)
		if _, _, err = tx.PutResponseUsage(domain.NewID(), domain.ResponseUsageRecord{SessionID: f.input.SessionID, ExecutionID: f.input.ExecutionID, AccountID: f.input.AccountID, ConnectionID: f.input.ConnectionID, ProviderID: f.input.Configuration.ProviderID, ModelID: f.input.Configuration.ModelID, Harness: domain.Codex, Version: domain.CodexProtocolVersion, ThreadID: string(f.thread), TurnID: string(f.turn), Sequence: 3, Usage: domain.NativeResponseUsage{ResponseDigest: strings.Repeat("b", 64), Counts: &counts, CostEvidence: domain.UsageCostMissing}}); err != nil {
			return nil, err
		}
		r, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		session.EstimatedCostBudget = &domain.EstimatedCostBudget{Currency: "USD", Threshold: threshold}
		return tx.Put(r.Kind, r.ID, r.Revision, r.ID, r.ProjectID, session)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func readTitleBudgetSession(t *testing.T, f *publicationFixture) domain.Session {
	t.Helper()
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](r)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestAutomaticTitleBudgetPreservesOriginalRecoverySettlement(t *testing.T) {
	for _, threshold := range []string{"0.0000975", "0.000097500000001"} {
		t.Run(threshold, func(t *testing.T) {
			f := recoveredAutomaticTitleFixture(t)
			seedTitleBudget(t, f, threshold)
			_, change := acceptRecovery(t, f)
			completeRecovery(t, f, change.ExecutionRecoveryJob)
			session := readTitleBudgetSession(t, f)
			if session.Outcome != domain.ExecutionSucceeded || session.ActiveExecutionID != "" || session.Recovery != domain.NoRecovery {
				t.Fatalf("title budget rolled back recovered result/cleanup: %+v", session)
			}
			record, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
			if err != nil {
				t.Fatal(err)
			}
			job, err := store.Decode[domain.Job](record)
			if err != nil || job.State != domain.JobSucceeded {
				t.Fatal("original job not settled", err)
			}
			if threshold == "0.0000975" {
				if session.TitleState != domain.TitleSkipped || session.TitleReason != domain.TitleReasonBudgetReached || session.TitleJobID != "" || session.TitleOperationID == "" {
					t.Fatalf("inclusive title budget did not skip: %+v", session)
				}
			} else if session.TitleState != domain.TitleQueued || session.TitleJobID == "" {
				t.Fatalf("below-budget title not queued: %+v", session)
			}
		})
	}
}

func TestQueuedAutomaticTitleBudgetRetiresWithoutClaim(t *testing.T) {
	f := recoveredAutomaticTitleFixture(t)
	_, change := acceptRecovery(t, f)
	completeRecovery(t, f, change.ExecutionRecoveryJob)
	session := readTitleBudgetSession(t, f)
	if session.TitleState != domain.TitleQueued {
		t.Fatal("fixture did not queue title")
	}
	seedTitleBudget(t, f, "0.0000975")
	record, err := claimTitleJob(context.Background(), f.service, f.input.MachineID, f.instance, f.device, session.TitleJobID)
	if err != nil {
		t.Fatal("budget retirement failed", err)
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil || job.State != domain.JobCanceled || job.FinishedAt == nil || job.Problem == nil || job.Problem.Code != domain.BudgetReached || job.InstanceID != "" {
		t.Fatalf("title inference was claimed or left queued: %+v %v", job, err)
	}
	current := readTitleBudgetSession(t, f)
	if current.TitleState != domain.TitleSkipped || current.TitleReason != domain.TitleReasonBudgetReached || current.TitleOperationID != session.TitleOperationID || current.TitleJobID != session.TitleJobID {
		t.Fatalf("retirement lost original title identity: %+v", current)
	}
}

func TestAutomaticTitleBudgetPreservesSuccessfulCompletionReceipt(t *testing.T) {
	f := publicationFixtureFromAuthority(t, newConfiguredAuthorityFixture(t, "http://127.0.0.1:1", func(input *domain.ExecutionJobInput) { input.Installation.ResolvedPath = "/private/codex" }, false))
	f.registerGrant(t)
	if _, err := f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Version: rpc.Version, Capabilities: []pb.WorkerCapability{pb.WorkerCapability_WORKER_CAPABILITY_AUTOMATIC_TITLES_CODEX_V1}})); err != nil {
		t.Fatal(err)
	}
	setAutomaticTitleWaiting(t, f)
	seedTitleBudget(t, f, "0.0000975")
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	terminal := f.event(domain.ExecutionTurnFinished, 3)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	request, _ := f.reportCompletion(t, f.completion())
	replay, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, request))
	if err != nil || !replay.Msg.Replayed {
		t.Fatal("original completion receipt lost", err)
	}
	session := readTitleBudgetSession(t, f)
	if session.Outcome != domain.ExecutionSucceeded || session.Execution == nil || !session.Execution.CleanupVerified || session.ActiveExecutionID != "" || session.TitleState != domain.TitleSkipped || session.TitleReason != domain.TitleReasonBudgetReached {
		t.Fatalf("budget rolled back original successful completion: %+v", session)
	}
	record, err := f.service.Store.Get(context.Background(), domain.JobKind, f.job)
	if err != nil {
		t.Fatal(err)
	}
	job, err := store.Decode[domain.Job](record)
	if err != nil || job.State != domain.JobSucceeded || job.FinishedAt == nil {
		t.Fatal("original job failed settlement", err)
	}
}
