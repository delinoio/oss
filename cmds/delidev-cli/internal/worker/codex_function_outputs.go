// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
	"github.com/delinoio/oss/cmds/delidev-cli/internal/harness/codex"
)

// Called under the publisher lock. Publication uses the original ordered outbox;
// neither a result nor a media marker sends a tool or claims completion.
func (c *CodexEventPublisher) publishFunctionOutput(ctx context.Context, event codex.Event) error {
	if !c.functionOutputSupported || event.FunctionOutput == nil || event.FunctionOutput.Validate() != nil || event.ItemID != event.FunctionOutput.NativeID {
		return publicationUncertain()
	}
	v := *event.FunctionOutput
	previous, known := c.functionOutputs[event.ItemID]
	if v.Stage == domain.CodexFunctionOutputStarted {
		if c.itemKnown(event.ItemID) || c.itemLimitReached() {
			return publicationUncertain()
		}
		v.ID = domain.NewID()
	} else {
		if !known || previous.Stage != domain.CodexFunctionOutputStarted || previous.Name != v.Name || !sameFunctionNamespace(previous.Namespace, v.Namespace) {
			return publicationUncertain()
		}
		v.ID = previous.ID
	}
	if err := c.publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionCodexFunctionOutputObserved, CodexFunctionOutput: &v}); err != nil {
		return err
	}
	// Retain only original identity/metadata, not duplicated text/native content.
	v.Output = domain.CodexFunctionOutputBody{}
	c.functionOutputs[event.ItemID] = v
	return nil
}
func sameFunctionNamespace(a, b *string) bool {
	return a == nil && b == nil || a != nil && b != nil && *a == *b
}
