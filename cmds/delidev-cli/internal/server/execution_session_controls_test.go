package server

import (
	"bytes"
	"context"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestNativeSessionControlsWaitForOwnedCleanupAndPreserveOutcome(t *testing.T) {
	for _, action := range []pb.SessionAction{pb.SessionAction_SESSION_ACTION_STOP, pb.SessionAction_SESSION_ACTION_ARCHIVE} {
		for _, phase := range []string{"unaccepted", "running", "terminal", "cleaned-success", "cleaned-failure"} {
			t.Run(action.String()+"/"+phase, func(t *testing.T) {
				ctx := context.Background()
				f := newPublicationFixture(t)
				client := delidevv1connect.NewSessionServiceClient(f.http.Client(), f.http.URL)
				outcome := domain.ExecutionSucceeded
				if phase == "cleaned-failure" {
					outcome = domain.ExecutionFailed
				}
				if phase != "unaccepted" {
					f.publish(t, f.event(domain.ExecutionThreadBound, 1))
					f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
				}
				cleaned := phase == "cleaned-success" || phase == "cleaned-failure"
				if phase == "terminal" || cleaned {
					e := f.event(domain.ExecutionTurnFinished, 3)
					e.Outcome = outcome
					f.publish(t, e)
				}
				if cleaned {
					completion := f.completion()
					completion.Outcome = outcome
					f.reportCompletion(t, completion)
				}
				before, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
				if err != nil {
					t.Fatal(err)
				}
				sr, err := f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				request := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: action}
				response, err := client.ControlSession(ctx, ownerRequest(f.service.Identity, request))
				if err != nil {
					t.Fatal(err)
				}
				var session domain.Session
				if err := domain.Decode(response.Msg.Change.Session.DocumentJson, &session); err != nil {
					t.Fatal(err)
				}
				if session.Dispatch != domain.DispatchPaused {
					t.Fatal("native control failed to pause before interruption")
				}
				if action == pb.SessionAction_SESSION_ACTION_ARCHIVE {
					want := domain.ArchivePending
					if cleaned {
						want = domain.Archived
					}
					if session.Archive != want {
						t.Fatal("Archive visibility preceded owned cleanup")
					}
				} else if session.Archive != domain.NotArchived {
					t.Fatal("Stop changed archive visibility")
				}
				after, err := f.service.Store.Get(ctx, domain.JobKind, f.job)
				if err != nil || before.Revision != after.Revision || !bytes.Equal(before.Data, after.Data) {
					t.Fatal("native control rewrote an accepted assignment or completed history")
				}
				err = f.service.Store.Read(ctx, func(tx *store.Tx) error {
					requested, err := tx.JobCancellationRequested(f.job)
					if requested == cleaned {
						t.Fatal("native cancellation scope confused active and cleaned jobs")
					}
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
				replayed, err := client.ControlSession(ctx, ownerRequest(f.service.Identity, request))
				if err != nil || !replayed.Msg.Change.Replayed || replayed.Msg.Change.Session.Revision != response.Msg.Change.Session.Revision {
					t.Fatal("control receipt repeated cancellation state changes")
				}
				if phase == "unaccepted" {
					_, err := f.client.ReportWork(ctx, ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), Problem: &pb.ErrorDetail{Code: string(domain.Canceled)}}))
					if err != nil {
						t.Fatal(err)
					}
				} else if !cleaned {
					if phase == "running" {
						outcome = domain.ExecutionStopped
						e := f.event(domain.ExecutionTurnFinished, 3)
						e.Outcome = outcome
						f.publish(t, e)
					}
					completion := f.completion()
					completion.Outcome = outcome
					f.reportCompletion(t, completion)
				}
				sr, err = f.service.Store.Get(ctx, domain.SessionKind, f.input.SessionID)
				if err != nil {
					t.Fatal(err)
				}
				session, err = store.Decode[domain.Session](sr)
				if err != nil {
					t.Fatal(err)
				}
				if phase == "unaccepted" {
					if session.Recovery != domain.NeedsRecovery || session.ActiveExecutionID == "" || session.PendingInputs != 1 || session.Archive == domain.Archived {
						t.Fatal("missing native acceptance/cleanup was promoted to completed control")
					}
					return
				}
				if session.Outcome != outcome || session.ActiveExecutionID != "" || !session.Execution.CleanupVerified {
					t.Fatal("control lost outcome or retained confirmed native ownership")
				}
				if action == pb.SessionAction_SESSION_ACTION_ARCHIVE {
					if session.Archive != domain.Archived {
						t.Fatal("verified native cleanup did not complete pending Archive")
					}
					restore := &pb.ControlSessionRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(sr.ID), ExpectedRevision: sr.Revision}, Action: pb.SessionAction_SESSION_ACTION_RESTORE}
					if _, err := client.ControlSession(ctx, ownerRequest(f.service.Identity, restore)); err != nil {
						t.Fatal(err)
					}
					replayed, err := client.ControlSession(ctx, ownerRequest(f.service.Identity, request))
					if err != nil || !replayed.Msg.Change.Replayed || domain.Decode(replayed.Msg.Change.Session.DocumentJson, &session) != nil || session.Archive != domain.NotArchived || session.Dispatch != domain.DispatchPaused || session.Outcome != outcome {
						t.Fatal("old Archive receipt undid paused restoration or changed outcome")
					}
				}
			})
		}
	}
}
