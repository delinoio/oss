package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

type codexArtifactPublication struct {
	ID        domain.ID
	Kind      domain.ArtifactKind
	Completed bool
}

func (c *CodexEventPublisher) itemKnown(native string) bool {
	_, message := c.messages[native]
	_, tool := c.tools[native]
	_, artifact := c.artifacts[native]
	return message || tool || artifact
}
func (c *CodexEventPublisher) itemLimitReached() bool {
	return len(c.messages)+len(c.tools)+len(c.artifacts) >= 10000
}

// Called under the event publisher lock; payloads are retained only by the
// synchronized outbox/server, not the in-memory native identity map.
func (c *CodexEventPublisher) publishArtifact(ctx context.Context, event codex.Event) error {
	if event.TurnID != c.turn {
		return publicationUncertain()
	}
	retained, known := c.artifacts[event.ItemID]
	update := domain.ExecutionArtifactUpdate{ID: retained.ID, NativeID: event.ItemID}
	var kind domain.ExecutionEventKind
	switch event.Kind {
	case codex.ArtifactStartedEvent, codex.ArtifactCompletedEvent:
		if event.Artifact == nil || event.Artifact.ID != event.ItemID {
			return publicationUncertain()
		}
		native := event.Artifact
		snapshot := domain.ArtifactSnapshot{Kind: map[codex.ArtifactKind]domain.ArtifactKind{codex.PlanArtifact: domain.PlanArtifact, codex.ReasoningArtifact: domain.ReasoningArtifact}[native.Kind], Text: native.Text, Summary: native.Summary, Content: native.Content}
		update.Snapshot = &snapshot
		if event.Kind == codex.ArtifactStartedEvent {
			if c.itemKnown(event.ItemID) || c.itemLimitReached() {
				return publicationUncertain()
			}
			retained = codexArtifactPublication{ID: domain.NewID(), Kind: snapshot.Kind}
			update.ID = retained.ID
			kind = domain.ExecutionArtifactStarted
		} else {
			if !known || retained.Completed || retained.Kind != snapshot.Kind {
				return publicationUncertain()
			}
			retained.Completed = true
			kind = domain.ExecutionArtifactCompleted
		}
	case codex.ArtifactDeltaEvent:
		if !known || retained.Completed || event.ArtifactDelta == nil {
			return publicationUncertain()
		}
		native := event.ArtifactDelta
		delta := domain.ArtifactDelta{Kind: map[codex.ArtifactDeltaKind]domain.ArtifactDeltaKind{codex.PlanTextDelta: domain.PlanTextDelta, codex.ReasoningSummaryDelta: domain.ReasoningSummaryDelta, codex.ReasoningContentDelta: domain.ReasoningContentDelta, codex.ReasoningSummaryAdded: domain.ReasoningSummaryAdded}[native.Kind], Index: native.Index, Text: native.Text}
		if delta.ArtifactKind() != retained.Kind {
			return publicationUncertain()
		}
		kind, update.Delta = domain.ExecutionArtifactDelta, &delta
	default:
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Artifact: &update}); err != nil {
		return err
	}
	c.artifacts[event.ItemID] = retained
	return nil
}

func (c *CodexEventPublisher) publishProgress(ctx context.Context, event codex.Event) error {
	if event.TurnID != c.turn || event.ItemID != "" {
		return publicationUncertain()
	}
	update := domain.ExecutionProgressUpdate{ID: domain.NewID()}
	switch event.Kind {
	case codex.TurnPlanEvent:
		if event.Plan == nil || event.Diff != nil {
			return publicationUncertain()
		}
		plan := &domain.NativePlan{Explanation: event.Plan.Explanation}
		if event.Plan.Steps != nil {
			plan.Steps = make([]domain.PlanStep, 0, len(event.Plan.Steps))
		}
		for _, step := range event.Plan.Steps {
			status := map[codex.PlanStepStatus]domain.PlanStepStatus{codex.PlanPending: domain.PlanPending, codex.PlanRunning: domain.PlanRunning, codex.PlanCompleted: domain.PlanCompleted}[step.Status]
			plan.Steps = append(plan.Steps, domain.PlanStep{Step: step.Step, Status: status})
		}
		update.Progress = domain.NativeProgress{Kind: domain.PlanProgress, Plan: plan}
	case codex.TurnDiffEvent:
		if event.Plan != nil || event.Diff == nil {
			return publicationUncertain()
		}
		update.Progress = domain.NativeProgress{Kind: domain.DiffProgress, Diff: event.Diff}
	default:
		return publicationUncertain()
	}
	return c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: &update})
}
