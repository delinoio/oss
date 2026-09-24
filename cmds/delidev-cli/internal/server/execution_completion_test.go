package server

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/rpc"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func (f *publicationFixture) completion() domain.ExecutionCompletion {
	return domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: f.thread, NativeTurnID: f.turn, LastSequence: 3, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
}

func TestNativeWorkerLossRetainsInputAndTerminalFacts(t *testing.T) {
	for _, operation := range []string{"replace", "revoke"} {
		for _, phase := range []string{"unaccepted", "accepted", "terminal"} {
			t.Run(operation+"/"+phase, func(t *testing.T) {
				ctx := context.Background()
				f := newPublicationFixture(t)
				if phase != "unaccepted" {
					f.publish(t, f.event(domain.ExecutionThreadBound, 1))
					f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
				}
				if phase == "terminal" {
					event := f.event(domain.ExecutionTurnFinished, 3)
					event.Outcome = domain.ExecutionSucceeded
					f.publish(t, event)
				}
				if operation == "replace" {
					_, err := f.service.Store.Mutate(ctx, domain.NewID(), "fixture.expire-worker", nil, func(tx *store.Tx) (any, error) {
						return nil, tx.SetWorkerInstance(f.input.MachineID, f.instance, time.Now().UTC().Add(-2*time.Minute))
					})
					if err != nil {
						t.Fatal(err)
					}
					_, err = f.client.AttachWorker(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.AttachWorkerRequest{RequestId: string(domain.NewID()), MachineId: string(f.input.MachineID), InstanceId: string(domain.NewID()), Version: rpc.Version}))
					if err != nil {
						t.Fatal(err)
					}
				} else {
					devices := delidevv1connect.NewDeviceServiceClient(f.http.Client(), f.http.URL)
					_, err := devices.RevokeDevice(ctx, ownerRequest(f.service.Identity, &pb.RevokeDeviceRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.device), ExpectedRevision: 1}}))
					if err != nil {
						t.Fatal(err)
					}
				}
				err := f.service.Store.Read(ctx, func(tx *store.Tx) error {
					_, session, err := sessionRecord(tx, f.input.SessionID)
					if err != nil {
						return err
					}
					if session.Recovery != domain.NeedsRecovery || session.Dispatch != domain.DispatchPaused || session.ActiveExecutionID != f.input.ExecutionID || (session.Execution != nil && session.Execution.CleanupVerified) {
						t.Fatal("lost Worker discarded unresolved native ownership")
					}
					if phase == "terminal" && session.Outcome != domain.ExecutionSucceeded {
						t.Fatal("cleanup uncertainty rewrote observed native success")
					}
					r, err := tx.Get(domain.QueueKind, f.input.InputID)
					if err != nil {
						return err
					}
					q, err := store.Decode[domain.QueuedInput](r)
					if err != nil {
						return err
					}
					if phase == "unaccepted" {
						if q.Delivery != domain.InputUncertain || session.PendingInputs != 1 {
							t.Fatal("unacknowledged native input was freed or made editable")
						}
					} else if q.Delivery != domain.InputAccepted || session.PendingInputs != 0 {
						t.Fatal("accepted input was returned to the pending queue")
					}
					r, err = tx.Get(domain.JobKind, f.job)
					if err != nil {
						return err
					}
					job, err := store.Decode[domain.Job](r)
					if err == nil && (job.State != domain.JobUncertain || job.FinishedAt != nil) {
						t.Fatal("Worker loss manufactured native completion")
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				_, err = f.call(f.requestEvent(t, f.event(domain.ExecutionInputAccepted, 2)))
				if err == nil {
					t.Fatal("old Worker retained publication authority")
				}
			})
		}
	}
}

func (f *publicationFixture) reportCompletion(t *testing.T, value domain.ExecutionCompletion) (*pb.ReportWorkRequest, *connect.Response[pb.ReportWorkResponse]) {
	t.Helper()
	raw, _ := json.Marshal(value)
	req := &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}
	response, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, req))
	if err != nil {
		t.Fatal(err)
	}
	return req, response
}

func TestNativeCompletionRequiresPublishedTerminalAndOwnedCleanup(t *testing.T) {
	for _, scenario := range []string{"success", "missing-terminal", "missing-cleanup", "wrong-turn", "stale-sequence"} {
		t.Run(scenario, func(t *testing.T) {
			f := newPublicationFixture(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			if scenario != "missing-terminal" {
				e := f.event(domain.ExecutionTurnFinished, 3)
				e.Outcome = domain.ExecutionSucceeded
				f.publish(t, e)
			}
			completion := f.completion()
			switch scenario {
			case "missing-cleanup":
				completion.CleanupVerified = false
			case "wrong-turn":
				completion.NativeTurnID = domain.NewID()
			case "stale-sequence":
				completion.LastSequence++
			}
			req, response := f.reportCompletion(t, completion)
			var job domain.Job
			if err := domain.Decode(response.Msg.Job.DocumentJson, &job); err != nil {
				t.Fatal(err)
			}
			r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			s, err := store.Decode[domain.Session](r)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "success" {
				if job.State != domain.JobSucceeded || !s.Execution.CleanupVerified || s.ActiveExecutionID != "" || s.Outcome != domain.ExecutionSucceeded || s.Dispatch != domain.DispatchPaused || s.Recovery != domain.NoRecovery {
					t.Fatal("verified completion lost cleanup or silently started another turn")
				}
			} else if job.State != domain.JobUncertain || s.Execution.CleanupVerified || s.ActiveExecutionID != f.input.ExecutionID || s.Recovery != domain.NeedsRecovery || s.Dispatch != domain.DispatchPaused {
				t.Fatal("invalid completion discarded uncertain native ownership")
			}
			replayed, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, req))
			if err != nil || !replayed.Msg.Replayed || replayed.Msg.Job.Revision != response.Msg.Job.Revision {
				t.Fatalf("completion receipt repeated native state changes: %v", err)
			}
		})
	}
}

func TestNativeFailedReportPreservesUncertainInputAndWorkspaceClaim(t *testing.T) {
	f := newPublicationFixture(t)
	_, err := f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.Unsupported)}}))
	if err != nil {
		t.Fatal(err)
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, _ := store.Decode[domain.Session](r)
	r, err = f.service.Store.Get(context.Background(), domain.QueueKind, f.input.InputID)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := store.Decode[domain.QueuedInput](r)
	if s.ActiveExecutionID != f.input.ExecutionID || s.Recovery != domain.NeedsRecovery || s.PendingInputs != 1 || q.Delivery != domain.InputUncertain {
		t.Fatal("failed native work was treated as editable or safely replayable input")
	}
}
