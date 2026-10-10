// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

// Only the safe projection enters the existing durable outbox. References and
// ciphertext never reach it; no output observation can dispatch native work.
func (c *CodexEventPublisher) publishFunctionOutput(ctx context.Context, event codex.Event) error {
	native := event.FunctionOutput
	if native == nil || event.TurnID != c.turn || native.ID != event.ItemID || native.Observation.Validate() != nil {
		return publicationUncertain()
	}
	retained, known := c.artifacts[event.ItemID]
	update := domain.ExecutionArtifactUpdate{ID: retained.ID, NativeID: event.ItemID, Snapshot: &domain.ArtifactSnapshot{Kind: domain.FunctionOutputArtifact, FunctionOutput: &native.Observation}}
	kind := domain.ExecutionArtifactStarted
	identity := native.Observation.Identity()
	if event.Kind == codex.FunctionOutputStartedEvent {
		if c.itemKnown(event.ItemID) || c.itemLimitReached() {
			return publicationUncertain()
		}
		retained = codexArtifactPublication{ID: domain.NewID(), Kind: domain.FunctionOutputArtifact, FunctionOutputIdentity: identity}
		update.ID = retained.ID
	} else if event.Kind == codex.FunctionOutputCompletedEvent {
		if !known || retained.Completed || retained.Kind != domain.FunctionOutputArtifact || retained.FunctionOutputIdentity != identity {
			return publicationUncertain()
		}
		retained.Completed = true
		kind = domain.ExecutionArtifactCompleted
	} else {
		return publicationUncertain()
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Artifact: &update}); err != nil {
		return err
	}
	c.artifacts[event.ItemID] = retained
	return nil
}
