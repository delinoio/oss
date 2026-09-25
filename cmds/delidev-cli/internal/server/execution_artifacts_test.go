package server

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/store"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/worker"
)

func TestExecutionArtifactsPreservePlanReplacementReasoningAndProgressHistory(t *testing.T) {
	f := newPublicationFixture(t)
	_, mapper := bindNativeMapper(t, f, publicationWorkerConfig(t, f))
	plan := &codex.Artifact{ID: "plan-item", Kind: codex.PlanArtifact}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactStartedEvent, ItemID: plan.ID, Artifact: plan})
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactDeltaEvent, ItemID: plan.ID, ArtifactDelta: &codex.ArtifactDelta{Kind: codex.PlanTextDelta, Text: "draft"}})
	plan.Text = "authoritative replacement"
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactCompletedEvent, ItemID: plan.ID, Artifact: plan})
	reasoning := &codex.Artifact{ID: "reasoning-item", Kind: codex.ReasoningArtifact, Summary: []string{}, Content: []string{}}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactStartedEvent, ItemID: reasoning.ID, Artifact: reasoning})
	summaryIndex, contentIndex := int64(2), int64(0)
	for _, delta := range []codex.ArtifactDelta{{Kind: codex.ReasoningSummaryAdded, Index: &summaryIndex}, {Kind: codex.ReasoningSummaryDelta, Index: &summaryIndex, Text: "native summary"}, {Kind: codex.ReasoningContentDelta, Index: &contentIndex, Text: "native content"}} {
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactDeltaEvent, ItemID: reasoning.ID, ArtifactDelta: &delta})
	}
	reasoning.Summary, reasoning.Content = []string{"", "", "native summary"}, []string{"native content"}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactCompletedEvent, ItemID: reasoning.ID, Artifact: reasoning})
	for _, status := range []codex.PlanStepStatus{codex.PlanCompleted, codex.PlanRunning} {
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnPlanEvent, Plan: &codex.PlanUpdate{Steps: []codex.PlanStep{{Step: "native step", Status: status}}}})
	}
	for _, diff := range []string{"-old\n+new\n", ""} {
		publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnDiffEvent, Diff: &diff})
	}
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.TurnCompletedEvent, Turn: &codex.Turn{ID: f.turn, Status: codex.TurnCompleted}})
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 6 {
		t.Fatalf("missing artifact/progress history: %v", err)
	}
	var latestPlan, latestDiff domain.ID
	for _, row := range rows {
		m, err := store.Decode[domain.ExecutionMessage](row)
		if err != nil || m.ExecutionID != f.input.ExecutionID || m.NativeThreadID != string(f.thread) || m.NativeTurnID != string(f.turn) || m.State != domain.MessageComplete || m.Text != "" || m.Tool != nil {
			t.Fatal("artifact/progress provenance was lost or conflated with text/tools")
		}
		switch m.Role {
		case domain.ArtifactMessage:
			a := m.Artifact
			if a == nil || a.Completed == nil || m.Progress != nil {
				t.Fatal("artifact completion snapshot was lost")
			}
			if a.Started.Kind == domain.PlanArtifact {
				if a.Started.Text != "" || len(a.Deltas) != 1 || a.Deltas[0].Sequence != 4 || a.Deltas[0].Delta.Text != "draft" || a.Completed.Text != "authoritative replacement" || m.LastSequence != 5 {
					t.Fatal("authoritative plan replaced draft evidence or required a prefix")
				}
			} else if a.Started.Kind != domain.ReasoningArtifact || len(a.Started.Summary) != 0 || len(a.Deltas) != 3 || *a.Deltas[0].Delta.Index != 2 || a.Deltas[0].Delta.Kind != domain.ReasoningSummaryAdded || a.Deltas[1].Delta.Text != "native summary" || *a.Deltas[2].Delta.Index != 0 || a.Completed.Summary[2] != "native summary" || a.Completed.Content[0] != "native content" || m.LastSequence != 10 {
				t.Fatal("native reasoning indices or observations were reconstructed or flattened")
			}
		case domain.ProgressMessage:
			if m.NativeID != "" || m.Artifact != nil || m.Progress == nil || m.FirstSequence != m.LastSequence || row.Revision != 1 {
				t.Fatal("turn progress gained fabricated item identity or mutable history")
			}
			if m.LastSequence == 12 {
				latestPlan = row.ID
				if m.Progress.Plan.Steps[0].Status != domain.PlanRunning || m.Progress.Plan.Explanation != nil {
					t.Fatal("plan revision was inferred from prior progress")
				}
			}
			if m.LastSequence == 14 {
				latestDiff = row.ID
				if m.Progress.Diff == nil || *m.Progress.Diff != "" {
					t.Fatal("empty latest diff lost its explicit observation")
				}
			}
		default:
			t.Fatal("native observation became another message family")
		}
	}
	r, err := f.service.Store.Get(context.Background(), domain.SessionKind, f.input.SessionID)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Decode[domain.Session](r)
	if err != nil || s.Execution.LatestPlanID != latestPlan || s.Execution.LatestDiffID != latestDiff || latestPlan == "" || latestDiff == "" || s.Execution.LastSequence != 15 || s.Outcome != domain.ExecutionSucceeded || s.Execution.CleanupVerified {
		t.Fatal("progress pointers/outcome changed or observations fabricated cleanup")
	}
}

func TestExecutionArtifactsRejectIncompleteMixedAndOversizedObservations(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	e := f.event(domain.ExecutionArtifactStarted, 3)
	e.Artifact = &domain.ExecutionArtifactUpdate{ID: id, NativeID: "owned-artifact", Snapshot: &domain.ArtifactSnapshot{Kind: domain.PlanArtifact}}
	f.publish(t, e)
	for _, bad := range []string{"duplicate-item", "tool-collision", "wrong-turn", "wrong-native", "changed-kind", "missing-terminal", "mixed-payload", "wrong-delta-kind", "oversized-publication"} {
		t.Run(bad, func(t *testing.T) {
			e := f.event(domain.ExecutionArtifactCompleted, 4)
			e.Artifact = &domain.ExecutionArtifactUpdate{ID: id, NativeID: "owned-artifact", Snapshot: &domain.ArtifactSnapshot{Kind: domain.PlanArtifact, Text: "final"}}
			switch bad {
			case "duplicate-item":
				e.Kind, e.Artifact.ID = domain.ExecutionArtifactStarted, domain.NewID()
			case "tool-collision":
				e.Kind, e.Artifact = domain.ExecutionToolStarted, nil
				e.Tool = &domain.ExecutionToolUpdate{ID: domain.NewID(), NativeID: "owned-artifact", Snapshot: toolCommand()}
			case "wrong-turn":
				e.NativeTurnID = string(domain.NewID())
			case "wrong-native":
				e.Artifact.NativeID = "foreign-artifact"
			case "changed-kind":
				e.Artifact.Snapshot = &domain.ArtifactSnapshot{Kind: domain.ReasoningArtifact, Summary: []string{}, Content: []string{}}
			case "missing-terminal":
				e.Kind, e.Artifact, e.Outcome = domain.ExecutionTurnFinished, nil, domain.ExecutionSucceeded
			case "mixed-payload":
				e.Artifact.Delta = &domain.ArtifactDelta{Kind: domain.PlanTextDelta, Text: "draft"}
			case "wrong-delta-kind":
				index := int64(0)
				e.Kind, e.Artifact.Snapshot = domain.ExecutionArtifactDelta, nil
				e.Artifact.Delta = &domain.ArtifactDelta{Kind: domain.ReasoningSummaryDelta, Index: &index}
			case "oversized-publication":
				e.Artifact.Snapshot = &domain.ArtifactSnapshot{Kind: domain.ReasoningArtifact, Summary: []string{strings.Repeat("x", domain.MaxMessageText), strings.Repeat("y", domain.MaxMessageText)}, Content: []string{}}
			}
			if _, err := f.call(f.requestEvent(t, e)); err == nil {
				t.Fatal("invalid artifact publication accepted")
			}
		})
	}
	last := uint64(3)
	for sequence := uint64(4); sequence < 10; sequence++ {
		e := f.event(domain.ExecutionArtifactDelta, sequence)
		e.Artifact = &domain.ExecutionArtifactUpdate{ID: id, NativeID: "owned-artifact", Delta: &domain.ArtifactDelta{Kind: domain.PlanTextDelta, Text: strings.Repeat("x", domain.MaxMessageText)}}
		if _, err := f.call(f.requestEvent(t, e)); err != nil {
			break
		}
		last = sequence
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || last < 4 || last > 7 || m.LastSequence != last || len(m.Artifact.Deltas) != int(last-3) || len(m.Artifact.Deltas[0].Delta.Text) != domain.MaxMessageText || m.Artifact.Completed != nil {
		t.Fatal("artifact bound consumed sequence or discarded prior evidence")
	}
	e = f.event(domain.ExecutionTurnFinished, last+1)
	e.Outcome = domain.ExecutionFailed
	f.publish(t, e)
}

func TestExecutionArtifactLostAcknowledgmentRetainsExactDelta(t *testing.T) {
	f := newPublicationFixture(t)
	cfg := publicationWorkerConfig(t, f)
	client := &losePublicationAck{WorkerServiceClient: f.client, t: t, path: filepath.Join(cfg.Root, "jobs", string(f.job), "publication.json"), dropAt: 4}
	cfg.Client = client
	publisher, mapper := bindNativeMapper(t, f, cfg)
	publishNativeEvent(t, f, mapper, codex.Event{Kind: codex.ArtifactStartedEvent, ItemID: "plan", Artifact: &codex.Artifact{ID: "plan", Kind: codex.PlanArtifact}})
	e := codex.Event{Kind: codex.ArtifactDeltaEvent, ItemID: "plan", ThreadID: f.thread, TurnID: f.turn, Correlated: true, ArtifactDelta: &codex.ArtifactDelta{Kind: codex.PlanTextDelta, Text: "once"}}
	if handled, err := mapper.PublishCore(context.Background(), e); !handled || err == nil {
		t.Fatal("lost artifact acknowledgment was not retained")
	}
	if _, err := mapper.PublishCore(context.Background(), e); err == nil {
		t.Fatal("uncertain artifact allowed another native publication")
	}
	if err := publisher.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := worker.OpenExecutionPublisher(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.ReplayPending(context.Background()); err != nil {
		t.Fatal(err)
	}
	rows, err := f.service.Store.List(context.Background(), store.Filter{Kind: domain.MessageKind, SessionID: f.input.SessionID, Limit: 10})
	if err != nil || len(rows) != 1 || len(client.calls) != 5 || client.calls[3] != client.calls[4] {
		t.Fatal("artifact replay changed identity")
	}
	m, err := store.Decode[domain.ExecutionMessage](rows[0])
	if err != nil || len(m.Artifact.Deltas) != 1 || m.Artifact.Deltas[0].Delta.Text != "once" || m.Artifact.Deltas[0].Sequence != 4 {
		t.Fatal("artifact replay duplicated the retained delta")
	}
}

func TestExecutionProgressKeepsImmutableHistoryAndRejectsMixedPayloads(t *testing.T) {
	f := newPublicationFixture(t)
	f.publish(t, f.event(domain.ExecutionThreadBound, 1))
	f.publish(t, f.event(domain.ExecutionInputAccepted, 2))
	id := domain.NewID()
	e := f.event(domain.ExecutionProgressObserved, 3)
	e.Progress = &domain.ExecutionProgressUpdate{ID: id, Progress: domain.NativeProgress{Kind: domain.PlanProgress, Plan: &domain.NativePlan{Steps: []domain.PlanStep{{Step: "step", Status: domain.PlanCompleted}}}}}
	receipt := f.publish(t, e)
	if r, err := f.call(receipt); err != nil || !r.Msg.Replayed {
		t.Fatalf("progress receipt replay failed: %v", err)
	}
	for _, bad := range []string{"rewrite-history", "missing-steps", "unknown-status", "mixed-diff", "wrong-turn", "mixed-event"} {
		e := f.event(domain.ExecutionProgressObserved, 4)
		e.Progress = &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.PlanProgress, Plan: &domain.NativePlan{Steps: []domain.PlanStep{{Step: "new", Status: domain.PlanRunning}}}}}
		switch bad {
		case "rewrite-history":
			e.Progress.ID = id
		case "missing-steps":
			e.Progress.Progress.Plan.Steps = nil
		case "unknown-status":
			e.Progress.Progress.Plan.Steps[0].Status = "failed"
		case "mixed-diff":
			diff := ""
			e.Progress.Progress.Diff = &diff
		case "wrong-turn":
			e.NativeTurnID = string(domain.NewID())
		case "mixed-event":
			e.Outcome = domain.ExecutionSucceeded
		}
		if _, err := f.call(f.requestEvent(t, e)); err == nil {
			t.Fatalf("invalid progress publication accepted: %s", bad)
		}
	}
	r, err := f.service.Store.Get(context.Background(), domain.MessageKind, id)
	if err != nil {
		t.Fatal(err)
	}
	m, err := store.Decode[domain.ExecutionMessage](r)
	if err != nil || r.Revision != 1 || m.Progress.Plan.Steps[0].Step != "step" || m.Progress.Plan.Steps[0].Status != domain.PlanCompleted {
		t.Fatal("progress mutation rewrote retained history")
	}
	e = f.event(domain.ExecutionTurnFinished, 4)
	e.Outcome = domain.ExecutionSucceeded
	f.publish(t, e)
}
