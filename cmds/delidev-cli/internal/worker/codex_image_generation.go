// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/imageinput"
)

func (c *CodexEventPublisher) publishImageGeneration(ctx context.Context, event codex.Event) error {
	if c.publisher == nil || !c.publisher.input.Configuration.Subscription || c.publisher.input.Configuration.SidechatPolicy != "" || event.ImageGeneration == nil || event.ItemID != event.ImageGeneration.ID || event.TurnID != c.turn {
		return publicationUncertain()
	}
	native := event.ImageGeneration
	retained, known := c.artifacts[event.ItemID]
	value := native.Observation
	kind := domain.ExecutionArtifactStarted
	if event.Kind == codex.ImageGenerationStartedEvent {
		if c.itemKnown(event.ItemID) || c.itemLimitReached() || value.Status != domain.ImageGenerationRunning || value.Validate() != nil || len(native.Bytes) != 0 {
			return publicationUncertain()
		}
		retained = codexArtifactPublication{ID: domain.NewID(), Kind: domain.ImageGenerationArtifact}
	} else {
		if !known || retained.Completed || retained.Kind != domain.ImageGenerationArtifact || value.Status == domain.ImageGenerationRunning {
			return publicationUncertain()
		}
		if value.Status == domain.ImageGenerationCompleted {
			p := c.publisher
			ref, err := (imageinput.Manager{Root: p.config.Root}).SaveGenerated(imageinput.GenerationOwner{JobID: p.job, ExecutionID: p.execution, InstanceID: p.config.Instance, MachineID: p.input.MachineID}, event.ItemID, native.Bytes)
			if err != nil {
				return err
			}
			value.Outputs = []domain.ImageAttachment{ref}
		} else if len(native.Bytes) != 0 {
			return publicationUncertain()
		}
		if value.Validate() != nil {
			return publicationUncertain()
		}
		retained.Completed = true
		kind = domain.ExecutionArtifactCompleted
	}
	snapshot := domain.ArtifactSnapshot{Kind: domain.ImageGenerationArtifact, ImageGeneration: &value}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: kind, Artifact: &domain.ExecutionArtifactUpdate{ID: retained.ID, NativeID: event.ItemID, Snapshot: &snapshot}}); err != nil {
		return err
	}
	c.artifacts[event.ItemID] = retained
	return nil
}
