// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

type functionOutputPublication struct {
	ID        domain.ID
	Digest    [32]byte
	Completed bool
}

func (c *CodexEventPublisher) publishFunctionOutput(ctx context.Context, event codex.Event) error {
	if event.TurnID != c.turn || event.FunctionOutput == nil || event.FunctionOutput.Validate() != nil {
		return publicationUncertain()
	}
	raw, err := json.Marshal(event.FunctionOutput)
	if err != nil {
		return publicationUncertain()
	}
	digest := sha256.Sum256(raw)
	retained, known := c.outputs[event.ItemID]
	if known {
		if retained.Completed || retained.Digest != digest || event.FunctionOutputStage != domain.FunctionOutputCompleted {
			return publicationUncertain()
		}
	} else {
		if c.itemKnown(event.ItemID) || c.itemLimitReached() {
			return publicationUncertain()
		}
		retained = functionOutputPublication{ID: domain.NewID(), Digest: digest}
	}
	if !c.functionOutputsSupported {
		// Older peers cannot validate this event kind, so the observation stays
		// private. The native identity and lifecycle are still retained so no
		// other item family can claim the ID and the shared item bound holds.
		retained.Completed = event.FunctionOutputStage == domain.FunctionOutputCompleted
		c.outputs[event.ItemID] = retained
		return nil
	}
	update := &domain.FunctionOutputUpdate{ID: retained.ID, NativeID: event.ItemID, Stage: event.FunctionOutputStage, Output: *event.FunctionOutput}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionFunctionOutputObserved, FunctionOutput: update}); err != nil {
		return err
	}
	retained.Completed = event.FunctionOutputStage == domain.FunctionOutputCompleted
	c.outputs[event.ItemID] = retained
	return nil
}
