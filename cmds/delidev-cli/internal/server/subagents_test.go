package server

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/security"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	pb "github.com/delinoio/oss/protos/gen/go/delidev/v1"
	"github.com/delinoio/oss/protos/gen/go/delidev/v1/delidevv1connect"
)

func TestSubagentPublicationReplayAndLateCompletionAfterParent(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	a := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexCollaborationSource, SourceID: "spawn-one"}
	count := "100"
	a.Usage = &domain.SubagentUsage{Scope: domain.SubagentCumulativeUsage, Total: &count}
	b := a
	b.ID = domain.NewID()
	b.NativeID = string(domain.NewID())
	b.ParentID = a.NativeID
	b.SourceID = "spawn-two"
	e := f.event(domain.ExecutionSubagentObserved, 3)
	e.Subagents = []domain.SubagentObservation{a, b}
	original := f.publish(t, e)
	if reply, err := f.call(original); err != nil || !reply.Msg.Replayed {
		t.Fatal("original child receipt did not replay", err)
	}
	resources := delidevv1connect.NewResourceServiceClient(http.DefaultClient, f.http.URL)
	rows, readErr := resources.ListResources(context.Background(), ownerRequest(f.service.Identity, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SUBAGENT, SessionId: string(f.input.SessionID), PageSize: 50}}))
	if readErr != nil || len(rows.Msg.Resources) != 2 || rows.Msg.Resources[0].Kind != pb.EntityKind_ENTITY_KIND_SUBAGENT {
		t.Fatal("authorized Connect child observation read failed", readErr)
	}
	if _, err := resources.ListResources(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ListResourcesRequest{Filter: &pb.Filter{Kind: pb.EntityKind_ENTITY_KIND_SUBAGENT}})); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("Worker acquired client child resource access", err)
	}
	terminal := f.event(domain.ExecutionTurnFinished, 4)
	terminal.Outcome = domain.ExecutionSucceeded
	f.publish(t, terminal)
	err := f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err != nil {
			return err
		}
		if session.Execution.Subagents.Closed() || session.Execution.CleanupVerified {
			t.Fatal("root terminal discarded live cleanup")
		}
		rows, err := tx.List(store.Filter{Kind: domain.SubagentKind, SessionID: f.input.SessionID, Limit: 50})
		if err == nil && len(rows) != 2 {
			t.Fatal("receipt duplicated tree entries")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	bad := f.event(domain.ExecutionSubagentObserved, 5)
	foreign := a
	foreign.ParentID = string(domain.NewID())
	bad.Subagents = []domain.SubagentObservation{foreign}
	if _, err := f.call(f.requestEvent(t, bad)); err == nil {
		t.Fatal("foreign late child accepted")
	}
	a.Status, b.Status = domain.SubagentCompleted, domain.SubagentCompleted
	a.Usage, b.Usage = nil, nil
	e = f.event(domain.ExecutionSubagentObserved, 5)
	e.Subagents = []domain.SubagentObservation{a, b}
	f.publish(t, e)
	err = f.service.Store.Read(context.Background(), func(tx *store.Tx) error {
		_, session, err := sessionRecord(tx, f.input.SessionID)
		if err == nil && (!session.Execution.Subagents.Closed() || session.Execution.LastSequence != 5 || session.Execution.CleanupVerified) {
			t.Fatal("late child publication changed cleanup or failed sequence")
		}
		if err != nil {
			return err
		}
		row, err := tx.Get(domain.SubagentKind, a.ID)
		if err != nil {
			return err
		}
		child, err := store.Decode[domain.SubagentRecord](row)
		if err == nil && (len(child.Sources) != 2 || child.Sources[0].Usage == nil || child.Sources[1].Usage != nil || child.Observation.Usage == nil) {
			t.Fatal("original usage report was erased or later status invented a counter report")
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestSubagentLiveChildRejectsCleanupWhileClosedTreeRequiresOriginalReport(t *testing.T) {
	for _, terminal := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "completed"}[terminal], func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			status := domain.SubagentRunning
			if terminal {
				status = domain.SubagentCompleted
			}
			e := f.event(domain.ExecutionSubagentObserved, 3)
			e.Subagents = []domain.SubagentObservation{{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: status, Source: domain.CodexCollaborationSource, SourceID: "original-spawn"}}
			f.publish(t, e)
			e = f.event(domain.ExecutionTurnFinished, 4)
			e.Outcome = domain.ExecutionSucceeded
			f.publish(t, e)
			completion := domain.ExecutionCompletion{Version: 1, ExecutionID: f.input.ExecutionID, InputID: f.input.InputID, NativeThreadID: domain.NativeIdentity(f.thread), NativeTurnID: domain.NativeIdentity(f.turn), LastSequence: 4, Outcome: domain.ExecutionSucceeded, CleanupVerified: true}
			raw, err := json.Marshal(completion)
			if err != nil {
				t.Fatal(err)
			}
			_, err = f.client.ReportWork(context.Background(), ownerRequest(security.Identity{Token: f.workerToken}, &pb.ReportWorkRequest{Mutation: &pb.Mutation{RequestId: string(domain.NewID()), Id: string(f.job), ExpectedRevision: 1}, MachineId: string(f.input.MachineID), InstanceId: string(f.instance), OutputJson: raw}))
			if err != nil {
				t.Fatal(err)
			}
			record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](record)
			if err != nil || session.Execution.CleanupVerified != terminal || session.Dispatch != domain.DispatchPaused || (!terminal && session.Recovery != domain.NeedsRecovery) {
				t.Fatal("live cleanup or unproved continuation was accepted", err)
			}
		})
	}
}
