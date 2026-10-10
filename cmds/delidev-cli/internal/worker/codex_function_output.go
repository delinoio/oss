// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

type codexFunctionOutputPublication struct {
	Name      string
	Namespace *string
	Completed bool
}

func (c *CodexEventPublisher) publishFunctionOutput(ctx context.Context, event codex.Event) error {
	v := event.FunctionOutput
	if event.TurnID != c.turn || v == nil || v.NativeItemID != event.ItemID || v.Validate() != nil {
		return publicationUncertain()
	}
	previous, known := c.functionOutputs[event.ItemID]
	if known {
		if previous.Completed || previous.Name != v.Name || (previous.Namespace == nil) != (v.Namespace == nil) || previous.Namespace != nil && *previous.Namespace != *v.Namespace {
			return publicationUncertain()
		}
	} else if c.itemKnown(event.ItemID) || c.itemLimitReached() {
		return publicationUncertain()
	}
	retained := codexFunctionOutputPublication{Name: v.Name}
	if v.Namespace != nil {
		namespace := *v.Namespace
		retained.Namespace = &namespace
	}
	if event.Kind == codex.FunctionOutputCompletedEvent {
		update := &domain.ExecutionProgressUpdate{ID: domain.NewID(), Progress: domain.NativeProgress{Kind: domain.CodexFunctionOutputProgress, FunctionOutput: v}}
		if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionProgressObserved, Progress: update}); err != nil {
			return err
		}
		retained.Completed = true
	} else if event.Kind != codex.FunctionOutputStartedEvent {
		return publicationUncertain()
	}
	c.functionOutputs[event.ItemID] = retained
	return nil
}
