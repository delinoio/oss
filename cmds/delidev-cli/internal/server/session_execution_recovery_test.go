package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func recoveryFixture(t *testing.T, outcome domain.ExecutionOutcome) *publicationFixture {
	t.Helper()
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	terminal := f.event(domain.ExecutionTurnFinished, 3)
	terminal.Outcome = outcome
	f.publish(t, terminal)
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-ready", nil, func(tx *store.Tx) (any, error) {
		sr, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return nil, err
		}
		prep := domain.NewID()
		if _, err := tx.PutJob(prep, 0, sr.ID, "", domain.Job{Type: domain.PrepareWorkspaceJob, MachineID: session.MachineID, State: domain.JobSucceeded, Input: json.RawMessage(`{}`), Output: json.RawMessage(`{}`), AcceptedAt: time.Now().UTC()}); err != nil {
			return nil, err
		}
		session.Preparation = &domain.SessionPreparation{JobID: prep, State: domain.PreparationReady}
		if _, err := tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, "", session); err != nil {
			return nil, err
		}
		return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().UTC().Add(-2*time.Minute))
	})
	if err != nil {
		t.Fatal(err)
	}
	replacement := domain.NewID()
	_, err = f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(replacement), Version: rpc.Version}))
	if err != nil {
		t.Fatal(err)
	}
	f.instance = replacement
	return f
}

func recoveryRequest(t *testing.T, f *publicationFixture) *pb.RecoverSessionExecutionRequest {
	t.Helper()
	var r store.Record
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		var err error
		r, err = tx.Get(domain.SessionKind, f.input.SessionID)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return &pb.RecoverSessionExecutionRequest{Mutation: &pb.Mutation{Id: string(r.ID), RequestId: string(domain.NewID()), ExpectedRevision: r.Revision}, ExpectedExecutionId: string(f.input.ExecutionID)}
}

func acceptRecovery(t *testing.T, f *publicationFixture) (*pb.RecoverSessionExecutionRequest, *pb.SessionChange) {
	t.Helper()
	request := recoveryRequest(t, f)
	client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
	response, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, request))
	if err != nil {
		t.Fatal(err)
	}
	return request, response.Msg.Change
}

func claimRecovery(t *testing.T, f *publicationFixture, resource *pb.Resource) (store.Record, domain.ExecutionRecoveryEvidence) {
	t.Helper()
	var claimed store.Record
	var expected domain.ExecutionRecoveryRequest
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.claim-recovery", resource.Id, func(tx *store.Tx) (any, error) {
		r, err := tx.Get(domain.JobKind, domain.ID(resource.Id))
		if err != nil {
			return nil, err
		}
		job, err := store.Decode[domain.Job](r)
		if err != nil {
			return nil, err
		}
		if err := domain.Decode(job.Input, &expected); err != nil {
			return nil, err
		}
		job.State, job.InstanceID = domain.JobClaimed, f.instance
		claimed, err = tx.PutJob(r.ID, r.Revision, r.SessionID, r.ProjectID, job)
		return nil, err
	})
	if err != nil {
		t.Fatal(err)
	}
	completion := expected.Completion
	completion.Version, completion.NativeCheckpointDigest = 2, strings.Repeat("ab", 32)
	return claimed, domain.ExecutionRecoveryEvidence{Version: 1, JobID: expected.JobID, ReportID: domain.NewID(), Completion: completion}
}

func TestExecutionRecoveryRetainsOutcomePauseAndReferenceRetries(t *testing.T) {
	for _, outcome := range []domain.ExecutionOutcome{domain.ExecutionSucceeded, domain.ExecutionFailed, domain.ExecutionStopped} {
		t.Run(string(outcome), func(t *testing.T) {
			f := recoveryFixture(t, outcome)
			req, change := acceptRecovery(t, f)
			var job domain.Job
			if domain.Decode(change.ExecutionRecoveryJob.DocumentJson, &job) != nil || job.Type != domain.RecoverExecutionJob || job.ParentID != f.job || bytes.Contains(job.Input, []byte(f.input.Input.Prompt)) {
				t.Fatal("recovery lost original metadata-only assignment")
			}
			client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			replay, err := client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, req))
			if err != nil || !replay.Msg.Change.Replayed || replay.Msg.Change.ExecutionRecoveryJob.Id != change.ExecutionRecoveryJob.Id {
				t.Fatal("acceptance replay created work", err)
			}
			_, duplicate := acceptRecovery(t, f)
			if duplicate.ExecutionRecoveryJob.Id != change.ExecutionRecoveryJob.Id {
				t.Fatal("parallel recovery created a second job")
			}
			claimed, evidence := claimRecovery(t, f, change.ExecutionRecoveryJob)
			raw, _ := json.Marshal(evidence)
			report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: string(claimed.ID), ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
			result, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, report))
			if err != nil {
				t.Fatal(err)
			}
			if domain.Decode(result.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobSucceeded {
				t.Fatalf("recovery rejected evidence: %+v", job.Problem)
			}
			result, err = f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, report))
			if err != nil || !result.Msg.Replayed {
				t.Fatal("completion retry failed", err)
			}
			replay, err = client.RecoverSessionExecution(context.Background(), ownerRequest(f.service.Identity, req))
			if err != nil || !replay.Msg.Change.Replayed {
				t.Fatal(err)
			}
			var session domain.Session
			if domain.Decode(replay.Msg.Change.Session.DocumentJson, &session) != nil || session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID != "" || session.NextExecutionIntent != "" || !session.Execution.CleanupVerified || session.Outcome != outcome {
				t.Fatalf("recovery resumed or lost terminal facts: %+v", session)
			}
			var original domain.Job
			var completion domain.ExecutionCompletion
			if domain.Decode(replay.Msg.Change.ExecutionJob.DocumentJson, &original) != nil || !original.State.Terminal() || domain.Decode(original.Output, &completion) != nil || completion != evidence.Completion {
				t.Fatal("original result not atomically reconciled")
			}
			_, err = f.call(f.requestEvent(t, f.event(domain.ExecutionInputAccepted, 2)))
			if err == nil {
				t.Fatal("old execution regained publication authority")
			}
		})
	}
}

func TestExecutionRecoveryRejectsStaleAndUnsettledEvidence(t *testing.T) {
	for _, scenario := range []string{"wrong-execution", "stale-revision", "worker-actor", "missing-terminal", "uncertain-input", "uncertain-steer", "waiting", "unconfirmed-response", "changed-cursor", "wrong-report", "missing-checkpoint", "worker-lost", "archive", "disconnect", "preserved-stop", "primary-request", "extra-accepted-input", "unobserved-question-claim", "unobserved-approval-claim", "open-interaction"} {
		t.Run(scenario, func(t *testing.T) {
			f := recoveryFixture(t, domain.ExecutionSucceeded)
			client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
			if scenario == "wrong-execution" || scenario == "stale-revision" || scenario == "worker-actor" {
				req := recoveryRequest(t, f)
				auth := f.service.Identity
				switch scenario {
				case "wrong-execution":
					req.ExpectedExecutionId = string(domain.NewID())
				case "stale-revision":
					req.Mutation.ExpectedRevision--
				case "worker-actor":
					auth.Token = f.workerToken
				}
				if _, err := client.RecoverSessionExecution(context.Background(), ownerRequest(auth, req)); err == nil {
					t.Fatal("invalid recovery accepted")
				}
				return
			}
			_, change := acceptRecovery(t, f)
			claimed, evidence := claimRecovery(t, f, change.ExecutionRecoveryJob)
			_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "fixture.recovery-change", scenario, func(tx *store.Tx) (any, error) {
				sr, session, err := sessionRecord(tx, f.input.SessionID)
				if err != nil {
					return nil, err
				}
				switch scenario {
				case "missing-terminal":
					session.Execution.Outcome = domain.ExecutionRunning
				case "changed-cursor":
					session.Execution.LastSequence++
				case "preserved-stop":
					session.Outcome = domain.ExecutionStopped
					session.Problem = domain.Fail(domain.Canceled, "Stopped.", "Resume explicitly.")
				case "primary-request":
					ir, err := tx.Get(domain.QueueKind, f.input.InputID)
					if err != nil {
						return nil, err
					}
					q, err := store.Decode[domain.QueuedInput](ir)
					if err != nil {
						return nil, err
					}
					q.NativeRequestID = domain.NewID()
					if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, sr.ID, "", q); err != nil {
						return nil, err
					}
				case "extra-accepted-input":
					if _, err := tx.Put(domain.QueueKind, domain.NewID(), 0, sr.ID, "", domain.QueuedInput{Sequence: 2, ContentRevision: 1, Prompt: "Foreign acceptance", Mode: domain.ExecuteMode, Delivery: domain.InputAccepted, ExecutionID: f.input.ExecutionID, NativeRequestID: domain.NewID()}); err != nil {
						return nil, err
					}
				case "waiting":
					session.Execution.Waiting = domain.NativeWaiting{UserInput: true}
				case "unconfirmed-response":
					session.Execution.UnconfirmedResponses = 1
				case "uncertain-input":
					ir, err := tx.Get(domain.QueueKind, f.input.InputID)
					if err != nil {
						return nil, err
					}
					q, err := store.Decode[domain.QueuedInput](ir)
					if err != nil {
						return nil, err
					}
					q.Delivery = domain.InputUncertain
					if _, err := tx.Put(ir.Kind, ir.ID, ir.Revision, sr.ID, "", q); err != nil {
						return nil, err
					}
				case "unobserved-question-claim", "unobserved-approval-claim", "open-interaction":
					interaction := domain.ExecutionInteraction{ExecutionID: f.input.ExecutionID, Closure: domain.InteractionTurnEnded}
					if scenario == "unobserved-question-claim" {
						interaction.Response = &domain.QuestionResponse{State: domain.QuestionResponseUncertain}
					}
					if scenario == "unobserved-approval-claim" {
						interaction.ApprovalResponse = &domain.ApprovalResponse{State: domain.ApprovalResponseClaimed}
					}
					if scenario == "open-interaction" {
						interaction.Closure = domain.InteractionOpen
					}
					if _, err := tx.Put(domain.InteractionKind, domain.NewID(), 0, sr.ID, "", interaction); err != nil {
						return nil, err
					}
				case "uncertain-steer":
					if _, err := tx.Put(domain.SteerKind, domain.NewID(), 0, sr.ID, "", domain.SteerAttempt{Version: 1, ExecutionID: f.input.ExecutionID, State: domain.SteerUncertain}); err != nil {
						return nil, err
					}
				case "archive":
					session.Archive = domain.ArchivePending
				case "disconnect":
					ar, err := tx.Get(domain.AccountKind, f.input.AccountID)
					if err != nil {
						return nil, err
					}
					account, err := store.Decode[domain.Account](ar)
					if err != nil {
						return nil, err
					}
					account.Connection = nil
					account.Health = domain.AccountUnverified
					if _, err := tx.Put(ar.Kind, ar.ID, ar.Revision, "", "", account); err != nil {
						return nil, err
					}
				case "worker-lost":
					return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().UTC().Add(-2*time.Minute))
				}
				_, err = tx.Put(sr.Kind, sr.ID, sr.Revision, sr.ID, "", session)
				return nil, err
			})
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "worker-lost" {
				_, err = f.client.AttachWorker(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), Version: rpc.Version}))
				if err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "wrong-report" {
				evidence.JobID = domain.NewID()
			}
			if scenario == "missing-checkpoint" {
				evidence.Completion.Version, evidence.Completion.NativeCheckpointDigest = 1, ""
			}
			raw, _ := json.Marshal(evidence)
			report := &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: string(claimed.ID), ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
			response, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, report))
			if scenario == "worker-lost" {
				if err == nil {
					t.Fatal("lost Worker reported")
				}
			} else if err != nil {
				t.Fatal(err)
			}
			var session domain.Session
			err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error { _, s, err := sessionRecord(tx, f.input.SessionID); session = s; return err })
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "archive" || scenario == "disconnect" || scenario == "preserved-stop" {
				if session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || (scenario == "archive" && session.Archive != domain.Archived) {
					t.Fatal("recovery failed to preserve archive/pause")
				}
				if scenario == "preserved-stop" && (session.Outcome != domain.ExecutionStopped || session.Problem == nil || session.Problem.Code != domain.Canceled) {
					t.Fatal("recovery erased independent stop/outcome")
				}
			} else {
				if session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || session.Execution.CleanupVerified {
					t.Fatal("uncertainty was cleared")
				}
				if response != nil {
					var job domain.Job
					if domain.Decode(response.Msg.Job.DocumentJson, &job) != nil || job.State != domain.JobUncertain {
						t.Fatal("mismatched proof was accepted")
					}
				}
			}
		})
	}
}

func TestExecutionRecoveryFailureAllowsNewExplicitInspection(t *testing.T) {
	f := recoveryFixture(t, domain.ExecutionFailed)
	_, first := acceptRecovery(t, f)
	claimed, evidence := claimRecovery(t, f, first.ExecutionRecoveryJob)
	bad := evidence
	bad.Completion.NativeTurnID = domain.NewID()
	raw, _ := json.Marshal(bad)
	_, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: string(claimed.ID), ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	_, second := acceptRecovery(t, f)
	if second.ExecutionRecoveryJob.Id == first.ExecutionRecoveryJob.Id {
		t.Fatal("explicit retry reused failed inspection")
	}
	claimed, evidence = claimRecovery(t, f, second.ExecutionRecoveryJob)
	raw, _ = json.Marshal(evidence)
	_, err = f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{Id: string(claimed.ID), ExpectedRevision: claimed.Revision, RequestId: string(domain.NewID())}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}))
	if err != nil {
		t.Fatal(err)
	}
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err == nil && (session.Recovery != domain.NoRecovery || session.Dispatch != domain.DispatchPaused || session.Outcome != domain.ExecutionFailed || session.Problem == nil || session.Problem.Code != domain.Unavailable) {
			t.Fatal("explicit recovery retry did not reconcile or erased the original failure")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}
