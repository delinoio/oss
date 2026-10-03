// SPDX-License-Identifier: Apache-2.0
package worker

import (
	"context"
	"encoding/json"

	"github.com/delinoio/oss/cmds/delidev-cli/internal/domain"
)

// Composer gate held. Each original task must have an acknowledged root tool
// before its separately verified child is published. Ordered facts use separate
// receipts; an initial task and its history never duplicate an identity in a batch.
func (c *OpenCodeEventPublisher) publishForegroundChildren(ctx context.Context, values []domain.SubagentObservation) error {
	b := c.text.binding
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, child := range values {
		ref := child.OpenCodeTool
		if ref == nil || child.ParentID != b.thread {
			return publicationUncertain()
		}
		tool := c.text.tools[ref.PartID]
		if tool == nil || tool.update.NativeParentID != ref.MessageID || tool.latest.Builtin == nil || tool.latest.Builtin.Name != domain.OpenCodeTask || tool.latest.Builtin.CallID != ref.CallID || tool.latest.Builtin.MetadataJSON == nil {
			return publicationUncertain()
		}
		metadata, err := domain.DecodeOpenCodeTaskMetadata([]byte(*tool.latest.Builtin.MetadataJSON))
		if err != nil || metadata.Parent != b.thread || metadata.Child != child.NativeID || metadata.Model.Provider != openCodeAPIProvider || metadata.Model.Model != b.publisher.input.Configuration.NativeModel {
			return publicationUncertain()
		}
		claims, err := b.readClaims()
		if err != nil || b.stage != openCodeAccepted || c.text.blocked || !b.validPublicationClaims(claims) {
			return publicationUncertain()
		}
		// The private native projection has no product-message UUID until this
		// join. Copy it before attaching that already acknowledged identity.
		raw, err := json.Marshal(child)
		if err != nil || domain.Decode(raw, &child) != nil {
			return publicationUncertain()
		}
		child.OpenCodeTool.ID = tool.update.ID
		next, err := domain.ApplySubagents(c.children, b.thread, []domain.SubagentObservation{child})
		if err != nil {
			return err
		}
		if err := b.publisher.Publish(ctx, domain.ExecutionEvent{Kind: domain.ExecutionSubagentObserved, NativeThreadID: b.thread, NativeTurnID: b.turn, Subagents: []domain.SubagentObservation{child}}); err != nil {
			return err
		}
		c.children = next
	}
	return nil
}
