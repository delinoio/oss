// SPDX-License-Identifier: Apache-2.0
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
	a := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "native-one", Usage: codexSubagentUsage()}
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

func TestSubagentBatchReusedHistoricalIdentityRollsBackEarlierWrites(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	original := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentCompleted, Source: domain.CodexHistorySource, SourceID: "original-history"}
	_, err := f.service.Store.Mutate(context.Background(), domain.NewID(), "subagent.fixture", nil, func(tx *store.Tx) (any, error) {
		return tx.Put(domain.SubagentKind, original.ID, 0, f.input.SessionID, "", domain.SubagentRecord{ExecutionID: domain.NewID(), RootID: string(f.thread), Observation: original})
	})
	if err != nil {
		t.Fatal(err)
	}
	first := original
	first.ID, first.NativeID, first.Status = domain.NewID(), string(domain.NewID()), domain.SubagentRunning
	reused := original
	reused.ID = domain.NewID()
	event := f.event(domain.ExecutionSubagentObserved, 3)
	event.Subagents = []domain.SubagentObservation{first, reused}
	if _, err := f.call(f.requestEvent(t, event)); err == nil {
		t.Fatal("historical native ownership was reused")
	}
	if _, err := f.service.Store.Get(context.Background(), domain.SubagentKind, first.ID); domain.SafeError(err).Code != domain.NotFound {
		t.Fatal("rejected batch partially retained its first child", err)
	}
	record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	session, err := store.Decode[domain.Session](record)
	if err != nil || session.Execution.LastSequence != 2 || len(session.Execution.Subagents) != 0 {
		t.Fatal("rejected batch advanced original progress", err)
	}
}

func TestSubagentOverlappingUsageCannotEnterRootLedger(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	rootUsage := f.event(domain.ExecutionUsageObserved, 3)
	rootUsage.ObservationID = domain.NewID()
	rootUsage.Usage = &domain.NativeTokenUsage{Total: reportedCounts(100), Last: reportedCounts(100)}
	f.publish(t, rootUsage)
	child := f.event(domain.ExecutionSubagentObserved, 4)
	child.Subagents = []domain.SubagentObservation{{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "native-child-usage", Usage: codexSubagentUsage()}}
	f.publish(t, child)
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.UsageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 {
		t.Fatal("child observation created an additive root usage row", err)
	}
	usage, err := store.Decode[domain.ExecutionUsageObservation](rows[0])
	if err != nil || usage.Usage.Total.Total == nil || *usage.Usage.Total.Total != 100 {
		t.Fatal("overlapping child usage changed the parent report", err)
	}
}

func TestSubagentNonPartialOutputRejectsWholePublication(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	first := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexCollaborationSource, SourceID: "original-spawn"}
	second := first
	second.ID, second.NativeID, second.SourceID = domain.NewID(), string(domain.NewID()), "original-child-output"
	for _, output := range []string{
		`{"native_message_id":"original-message","text":"recent subset"}`,
		`{"native_message_id":"original-message","text":"recent subset","partial":false}`,
	} {
		t.Run(output, func(t *testing.T) {
			second.Output = &domain.SubagentOutput{}
			if err := json.Unmarshal([]byte(output), second.Output); err != nil {
				t.Fatal(err)
			}
			event := f.event(domain.ExecutionSubagentObserved, 3)
			event.Subagents = []domain.SubagentObservation{first, second}
			if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("non-partial output did not return an ownership conflict", err)
			}
			for _, child := range []domain.SubagentObservation{first, second} {
				if _, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID); domain.SafeError(err).Code != domain.NotFound {
					t.Fatal("rejected output partially retained a child", err)
				}
			}
			record, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
			if err != nil {
				t.Fatal(err)
			}
			session, err := store.Decode[domain.Session](record)
			if err != nil || session.Execution.LastSequence != 2 || len(session.Execution.Subagents) != 0 {
				t.Fatal("rejected output advanced execution ownership", err)
			}
		})
	}
	second.Output.Partial = true
	event := f.event(domain.ExecutionSubagentObserved, 3)
	event.Subagents = []domain.SubagentObservation{first, second}
	f.publish(t, event)
}

func TestSubagentRequestedModelCannotBeReplaced(t *testing.T) {
	f := newPublicationFixture(t)
	f.registerGrant(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	model := "original-model"
	child := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexCollaborationSource, SourceID: "original-spawn", RequestedModel: &model}
	event := f.event(domain.ExecutionSubagentObserved, 3)
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)
	changed := "different-model"
	child.RequestedModel = &changed
	event.Sequence, event.Subagents = 4, []domain.SubagentObservation{child}
	if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("later spawn replaced original requested model", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.SubagentRecord](row)
	if err != nil || retained.LastSequence != 3 || retained.Observation.RequestedModel == nil || *retained.Observation.RequestedModel != model {
		t.Fatal("rejected model publication altered original evidence", err)
	}
}

func TestClaudeSubagentRequestedModelMatchesOriginalProposal(t *testing.T) {
	f, children, sequence := claudeSubagentPublicationFixtureWithProposal(t, "{}", `{"model":"original-model"}`)
	child := children[0]
	forged := "forged-model"
	child.RequestedModel = &forged
	event := f.event(domain.ExecutionSubagentObserved, sequence+1)
	event.Subagents = []domain.SubagentObservation{child}
	if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("Claude task accepted a model not present in its original proposal", err)
	}

	f, children, sequence = claudeSubagentPublicationFixture(t)
	child = children[0]
	event = f.event(domain.ExecutionSubagentObserved, sequence+1)
	event.Subagents = []domain.SubagentObservation{child}
	f.publish(t, event)
	child.Source, child.SourceID, child.Task = domain.ClaudeContentSource, "content-forged-model", nil
	child.RequestedModel = &forged
	event = f.event(domain.ExecutionSubagentObserved, sequence+2)
	event.Subagents = []domain.SubagentObservation{child}
	if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
		t.Fatal("Claude content populated an originally unavailable requested model", err)
	}
	row, err := f.service.Store.Get(context.Background(), domain.SubagentKind, child.ID)
	if err != nil {
		t.Fatal(err)
	}
	retained, err := store.Decode[domain.SubagentRecord](row)
	if err != nil || retained.Observation.RequestedModel != nil {
		t.Fatal("rejected model changed the retained unavailable value", err)
	}
}

func TestSubagentCodexRejectsClaudeMetadataAtomically(t *testing.T) {
	for _, name := range []string{"task", "tool", "tools"} {
		t.Run(name, func(t *testing.T) {
			f := newPublicationFixture(t)
			f.registerGrant(t)
			f.publish(t, f.event(domain.ExecutionThreadBound, 1))
			f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
			first := domain.SubagentObservation{ID: domain.NewID(), NativeID: string(domain.NewID()), ParentID: string(f.thread), Status: domain.SubagentRunning, Source: domain.CodexHistorySource, SourceID: "native-history"}
			second := first
			second.ID, second.NativeID = domain.NewID(), string(domain.NewID())
			switch name {
			case "task":
				second.Task = &domain.SubagentTask{}
			case "tool":
				second.Tool = &domain.ClaudeToolReference{}
			case "tools":
				second.Tools = []domain.SubagentTool{{NativeID: "invented-native-tool", Name: "Agent"}}
			}
			event := f.event(domain.ExecutionSubagentObserved, 3)
			event.Subagents = []domain.SubagentObservation{first, second}
			if _, err := f.call(f.requestEvent(t, event)); connect.CodeOf(err) != connect.CodeAborted {
				t.Fatal("Codex retained fabricated Claude ownership metadata", err)
			}
			if _, err := f.service.Store.Get(context.Background(), domain.SubagentKind, first.ID); domain.SafeError(err).Code != domain.NotFound {
				t.Fatal("foreign harness metadata partially published its batch", err)
			}
		})
	}
}
