// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

// publishWebSearch retains one original native search, open-page or find call.
// The frozen execution mode must match the observation; no result, citation or
// completion is synthesized, and a disabled mode never publishes a call.
func (c *CodexEventPublisher) publishWebSearch(ctx context.Context, event codex.Event) error {
	if c.publisher == nil || event.WebSearch == nil || event.ItemID != event.WebSearch.ID || event.TurnID != c.turn {
		return publicationUncertain()
	}
	mode := c.publisher.input.NativeWebSearchMode
	value := event.WebSearch.Observation
	if mode == "" || mode == domain.WebSearchDisabled || value.Mode != mode || value.Validate() != nil {
		return publicationUncertain()
	}
	retained, known := c.artifacts[event.ItemID]
	kind := domain.ExecutionArtifactStarted
	if event.Kind == codex.WebSearchStartedEvent {
		if c.itemKnown(event.ItemID) || c.itemLimitReached() {
			return publicationUncertain()
		}
		retained = codexArtifactPublication{ID: domain.NewID(), Kind: domain.WebSearchArtifact}
	} else {
		if !known || retained.Completed || retained.Kind != domain.WebSearchArtifact {
			return publicationUncertain()
		}
		retained.Completed = true
		kind = domain.ExecutionArtifactCompleted
	}
	snapshot := domain.ArtifactSnapshot{Kind: domain.WebSearchArtifact, WebSearch: &value}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Artifact: &domain.ExecutionArtifactUpdate{ID: retained.ID, NativeID: event.ItemID, Snapshot: &snapshot}}); err != nil {
		return err
	}
	c.artifacts[event.ItemID] = retained
	return nil
}
